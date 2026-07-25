package server

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/zsearch/v2/pkg/translate"
	"golang.org/x/text/language"
)

// searchPage handles the search request and renders the search grid, table, or list page.
func (ctrl *Controller) searchPage(c *gin.Context, page string) {
	// determine language from parameter, fallback to German if not available
	var lang = c.Param("lang")
	if !ctrl.langAvailable(lang) {
		lang = "de"
	}
	// load and initialize the HTML template for the search grid
	templateName := "search_grid.gohtml"
	files := ctrl.getTemplatesByPrefix("search_", "index.gohtml", "impressum.gohtml", "kontakt.gohtml", "detail.gohtml", "zoom.gohtml")
	gridTemplate, err := ctrl.loadHTMLTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}
	// parse the search query string into filter and query components
	searchString := c.Query("search")
	filterStrings, queryString, err := parseQuery(searchString)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot parse query '%s'", searchString)
		// c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot parse query '%s': %v", searchString, err))
		queryString = searchString
	}

	// extract and process search parameters like cursor, ki, collections, catalogs, and vocabulary
	cursorString := c.Query("cursor")
	fromString := c.Query("from")
	_from, _ := strconv.Atoi(fromString)
	from := int64(_from)
	pageSizeString := c.Query("pagesize")
	_pageSize, _ := strconv.Atoi(pageSizeString)
	pageSize := int64(_pageSize)
	if cursorString == "" && pageSize == 0 {
		pageSize = 36
	}
	ki := c.Request.URL.Query().Has("ki")
	parseIDs := func(s string) []int {
		var ids []int
		for _, part := range strings.Split(s, ",") {
			id, err := strconv.Atoi(part)
			if err != nil || id == 0 {
				continue
			}
			ids = append(ids, id)
		}
		return ids
	}
	searchType := c.Query("searchtype")
	collectionIDs := parseIDs(c.Query("collections"))
	catalogIDs := parseIDs(c.Query("catalogs"))
	mediaIDs := parseIDs(c.Query("medias"))
	collectionsString := c.Query("collections")
	catalogsString := c.Query("catalogs")
	mediasString := c.Query("medias")
	vocabularyString := c.Query("vocabulary")
	vocabularyIDs := []string{}
	for _, part := range strings.Split(vocabularyString, ",") {
		vocabularyID := strings.TrimSpace(part)
		if part == "" {
			continue
		}
		vocabularyIDs = append(vocabularyIDs, vocabularyID)
	}
	// define facets for vocabulary, collections, and catalogs for the search request
	vocFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "vocabulary",
			Field:       "tags.keyword",
			Size:        1200,
			MinDocCount: 1,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "tags.keyword",
				Values: vocabularyIDs,
				And:    true,
			},
		},
	}
	if len(ctrl.facetInclude) > 0 {
		vocFacet.Term.Include = append(vocFacet.Term.Include, ctrl.facetInclude...)
	}
	if len(ctrl.facetExclude) > 0 {
		vocFacet.Term.Exclude = append(vocFacet.Term.Exclude, ctrl.facetExclude...)
	}
	createFacet := func(name, field string, ctrlList []*CollFacetType, selectedIDs []int, idPrefix string) *client.InFacet {
		facet := &client.InFacet{
			Term: &client.InFacetTerm{
				Name:        name,
				Field:       field,
				Size:        200,
				MinDocCount: 0,
				Include:     []string{},
				Exclude:     []string{},
			},
		}

		// Special case for catalogs when no IDs are selected
		if name != "collections" && len(selectedIDs) == 0 {
			facet.Query = &client.InFilter{
				ExistsTerm: &client.InFilterExistsTerm{
					Field: "signature",
				},
			}
		} else {
			facet.Query = &client.InFilter{
				BoolTerm: &client.InFilterBoolTerm{
					Field:  field,
					Values: []string{},
					And:    false,
				},
			}
		}

		for _, item := range ctrlList {
			parts := strings.SplitN(item.Identifier, ":", 2)
			if len(parts) != 2 {
				continue
			}
			val := strings.Trim(parts[1], "\" ")
			facet.Term.Include = append(facet.Term.Include, val)
			if len(selectedIDs) == 0 || slices.Contains(selectedIDs, int(item.Id)) {
				if facet.Query.BoolTerm != nil {
					if parts[0] == idPrefix {
						facet.Query.BoolTerm.Values = append(facet.Query.BoolTerm.Values, val)
					} else {
						ctrl.logger.Error().Msgf("unknown %s identifier '%s' (expected prefix '%s')", name, item.Identifier, idPrefix)
					}
				}
			}
		}
		return facet
	}

	collFacet := createFacet("collections", "category.keyword", ctrl.collections, collectionIDs, "cat")
	catFacet := createFacet("catalogs", "catalog.keyword", ctrl.catalogs, catalogIDs, "catalog")
	mediaFacet := createFacet("medias", "mediatype.keyword", ctrl.medias, mediaIDs, "mediatypes.keyword")

	var result *client.Search
	/*
		var embedding64 = []float64{}
		//queryString := searchString
		if ki && searchString != "" {
			embedding, err := ctrl.embeddings.CreateEmbedding(searchString, oai.SmallEmbedding3)
			if err != nil {
				ctrl.logger.Error().Err(err).Msgf("cannot create embedding for '%s'", searchString)
				c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot create embedding for '%s': %v", searchString, err))
				return
			}
			for _, v := range embedding.Embedding {
				embedding64 = append(embedding64, float64(v))
			}
			queryString = ""
		}
	*/
	// determine sort field and order from query parameters
	var sortField = c.Query("sortField")
	var sortOrder = c.Query("sortOrder")
	var sort = []*client.SortField{}
	if sortField != "" {
		sort = append(sort, &client.SortField{
			Field: sortField,
			Order: sortOrder,
		})
	}
	// apply access control filters based on user groups
	filter := append([]*client.InFilter{}, ctrl.baseFilter...)
	/*
		user := GetUser(c)
			for _, f := range filter {
				if f.BoolTerm != nil {
					if f.BoolTerm.Field == "acl.content.keyword" {
						f.BoolTerm.Values = user.Groups
					}
				}
			}
	*/
	// add field-specific filters to the search request
	if len(filterStrings) > 0 {
		for field, value := range filterStrings {
			internalField, ok := ctrl.fieldMapping[field]
			if !ok {
				ctrl.logger.Error().Msgf("unknown field '%s'", field)
				c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown field '%s'", field))
				return
			}
			filter = append(filter, &client.InFilter{
				BoolTerm: &client.InFilterBoolTerm{
					Field:  internalField,
					Values: []string{strings.Trim(value, "\" ")},
					And:    true,
				},
			})
		}
	}
	// execute the search request using the GraphQL client
	var fromPtr, pageSizePtr *int64
	var cursorPtr *string
	if cursorString != "" {
		cursorPtr = &cursorString
	} else {
		fromPtr = &from
		pageSizePtr = &pageSize
	}
	result, err = ctrl.client.Search(c, searchType, queryString, []*client.InFacet{collFacet, catFacet, mediaFacet, vocFacet}, filter, nil, fromPtr, pageSizePtr, cursorPtr, sort)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", searchString)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", searchString, err))
		return
	}

	type facetType struct {
		ID      int    `json:"id,omitzero"`
		Name    string `json:"name"`
		Count   int    `json:"count"`
		Checked bool   `json:"checked"`
	}
	fillFacet := func(values []*client.FacetValueFragment, ctrlList []*CollFacetType, selectedIDs []int, target *[]*facetType) {
		for _, val := range values {
			strVal := val.GetFacetValueString()
			if strVal == nil {
				continue
			}
			facetStr := strVal.GetStrVal()
			cf := &facetType{
				Count: int(strVal.GetCount()),
			}
			for _, item := range ctrlList {
				parts := strings.SplitN(item.Identifier, ":", 2)
				if len(parts) != 2 {
					continue
				}
				cVal := strings.Trim(parts[1], "\" ")
				if cVal == facetStr {
					cf.ID = int(item.Id)
					cf.Name = item.Title
					cf.Checked = slices.Contains(selectedIDs, int(item.Id))
					*target = append(*target, cf)
				}
			}
		}
	}
	type edge struct {
		Edge             *client.Search_Search_Edges `json:"edge"`
		Title            *translate.MultiLangString  `json:"title"`
		Persons          string                      `json:"persons"`
		Type             string                      `json:"type"`
		Date             string                      `json:"date"`
		PersonRole       map[string][]string
		ShowContent      bool
		ProtectedContent bool
	}
	// prepare search URL and parameters for the template data
	currentSearchURL := url.Values{}
	if searchType != "" {
		currentSearchURL.Set("searchtype", searchType)
	}
	if searchString != "" {
		currentSearchURL.Set("search", searchString)
	}
	if collectionsString != "" {
		currentSearchURL.Set("collections", collectionsString)
	}
	if catalogsString != "" {
		currentSearchURL.Set("catalogs", catalogsString)
	}
	if mediasString != "" {
		currentSearchURL.Set("medias", mediasString)
	}
	if vocabularyString != "" {
		currentSearchURL.Set("vocabulary", vocabularyString)
	}
	var searchParams string
	if len(currentSearchURL) > 0 {
		searchParams = "?" + currentSearchURL.Encode()
	}
	_, isExhibition := c.GetQuery("exhibition")

	// populate template data with search results, facets, and request metadata
	data := struct {
		baseData
		//Result           *client.Search_Search      `json:"result"`
		TotalCount       int                      `json:"totalCount"`
		PageInfo         *client.PageInfoFragment `json:"pageInfo"`
		Edges            []*edge                  `json:"edges"`
		MediaserverBase  string                   `json:"mediaserverBase"`
		RequestQuery     *queryData               `json:"request"`
		CollectionFacets []*facetType             `json:"collectionFacets"`
		CatalogFacets    []*facetType             `json:"catalogFacets"`
		MediaFacets      []*facetType             `json:"mediaFacets"`
		VocabularyFacets map[string][]*facetType  `json:"vocabularyFacets"`
	}{
		//Result:          result.GetSearch(),
		MediaserverBase: ctrl.mediaserverBase,
		PageInfo:        result.GetSearch().GetPageInfo(),
		baseData: baseData{
			Mode:       ctrl.mode,
			Lang:       lang,
			Exhibition: isExhibition,
			KI:         ki,
			//Search:     template.URL(currentSearchURL.Encode()),
			//			Search:       template.URL(fmt.Sprintf("%s/search/%s%s", ctrl.searchAddr, lang, searchParams)),
			//			SearchParams: searchParams,
			Cursor:     cursorString,
			Params:     template.URL(strings.TrimLeft(searchParams, "?&	")),
			RootPath:   "../",
			SearchAddr: ctrl.searchAddr,
			DetailAddr: ctrl.detailAddr,
			Page:       page,
			LoginURL:   ctrl.loginURL,
			Self:       fmt.Sprintf("%s%s", ctrl.externalAddr, c.Request.URL.Path),
			User:       GetUser(c),
		},
		TotalCount: int(result.GetSearch().GetTotalCount()),
		RequestQuery: &queryData{
			Search: searchString,
		},
		CollectionFacets: []*facetType{},
		CatalogFacets:    []*facetType{},
		MediaFacets:      []*facetType{},
		VocabularyFacets: map[string][]*facetType{},
	}
	if data.baseData.User.IsLoggedIn() {
		data.baseData.DetailAddr = data.baseData.SearchAddr
	}
	// process search result edges, extract titles, persons, and roles
	for _, e := range result.GetSearch().GetEdges() {
		ne := &edge{
			Edge:       e,
			Title:      &translate.MultiLangString{},
			Type:       emptyIfNil(e.Base.GetType()),
			Date:       emptyIfNil(e.Base.GetDate()),
			PersonRole: map[string][]string{},
			//ShowContent:      false,
			//ProtectedContent: false,
		}
		for _, t := range e.Base.GetTitle() {
			ne.Title.Set(t.Value, language.MustParse(t.Lang), t.Translated)
		}
		var firstPerson string
		for _, p := range e.Base.GetPerson() {
			if firstPerson == "" {
				firstPerson = p.GetName()
			}
			if ne.Persons != "" {
				ne.Persons += "; "
			}
			ne.Persons += p.GetName()
			var role = "author"
			if p.GetRole() != nil {
				role = *p.GetRole()
			}
			if _, ok := ne.PersonRole[role]; !ok {
				ne.PersonRole[role] = []string{}
			}
			ne.PersonRole[role] = append(ne.PersonRole[role], p.GetName())
		}
		if len(ne.Persons) > 30 && len(e.Base.GetPerson()) > 1 {
			ne.Persons = firstPerson + " et al."
		}
		data.Edges = append(data.Edges, ne)
	}
	// process search facets and group them into vocabulary, collections, and catalogs
	for _, facet := range result.GetSearch().GetFacets() {
		switch facet.GetName() {
		case "vocabulary":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				parts := strings.Split(facetStr, ":")
				// 16:9  4:3
				if len(parts) == 2 && len(parts[1]) < 3 {
					parts = []string{facetStr}
				}
				var name string
				var parent = "generic"
				if len(parts) == 1 {
					name = parts[0]
				} else if len(parts) == 3 {

					if val.GetFacetValueInt() == nil || val.GetFacetValueInt().GetIntVal() == 0 {
						if !strings.HasPrefix(parts[1], "voc_") {
							continue
						}
					}
					parent = parts[1] // slug.MakeLang(parts[1], "de")
					name = parts[2]
					if _, ok := data.VocabularyFacets[parent]; !ok {
						data.VocabularyFacets[parts[1]] = []*facetType{}
					}
				} else {
					continue
				}
				data.VocabularyFacets[parent] = append(data.VocabularyFacets[parent], &facetType{
					Count:   int(strVal.GetCount()),
					Name:    name,
					Checked: slices.Contains(vocabularyIDs, facetStr),
				})
			}

		case "collections":
			fillFacet(facet.GetValues(), ctrl.collections, collectionIDs, &data.CollectionFacets)
		case "catalogs":
			fillFacet(facet.GetValues(), ctrl.catalogs, catalogIDs, &data.CatalogFacets)
		case "medias":
			fillFacet(facet.GetValues(), ctrl.medias, mediaIDs, &data.MediaFacets)
		}
	}
	var str string
	/*
		for _, vf := range data.VocabularyFacets {
			for _, v := range vf {
				str += fmt.Sprintf("\"%s\" = \"%s\"\n", v.Name, strings.TrimPrefix(v.Name, "voc_"))
			}

		}

	*/
	for v, _ := range data.VocabularyFacets {
		str += fmt.Sprintf("\"%s\" = \"%s\"\n", v, strings.TrimPrefix(v, "voc_"))

	}

	// render the template with the populated data
	if err := gridTemplate.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}
