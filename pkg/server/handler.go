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

func (ctrl *Controller) impressumPage(c *gin.Context) {
	lang := ctrl.getLang(c)

	templateName := "impressum.gohtml"
	files := ctrl.getTemplatesByPrefix("impressum_", "index.gohtml", "kontakt.gohtml", "search_grid.gohtml", "detail.gohtml", "zoom.gohtml")
	impressumTemplate, err := ctrl.loadHTMLTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}

	type tplData struct {
		baseData
		Collections map[int64]*CollFacetType `json:"collections"`
		Catalogs    map[int64]*CollFacetType `json:"catalogs"`
	}
	var data = &tplData{
		Collections: map[int64]*CollFacetType{},
		Catalogs:    map[int64]*CollFacetType{},
		baseData:    ctrl.getBaseData(c, lang, "../../"),
	}
	collFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "collections",
			Field:       "category.keyword",
			Size:        200,
			MinDocCount: 0,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "tags.keyword",
				Values: []string{},
				And:    false,
			},
		},
	}
	for _, coll := range ctrl.collections {
		parts := strings.SplitN(coll.Identifier, ":", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.Trim(parts[1], "\" ")
		collFacet.Term.Include = append(collFacet.Term.Include, val)
		switch parts[0] {
		case "cat":
			collFacet.Query.BoolTerm.Values = append(collFacet.Query.BoolTerm.Values, val)
		default:
			ctrl.logger.Error().Err(err).Msgf("unknown collection identifier '%s'", coll.Identifier)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown collection identifier '%s'", coll.Identifier))
			return
		}
	}
	catFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "catalogs",
			Field:       "catalog.keyword",
			Size:        200,
			MinDocCount: 0,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "tags.keyword",
				Values: []string{},
				And:    false,
			},
		},
	}
	for _, cat := range ctrl.catalogs {
		parts := strings.SplitN(cat.Identifier, ":", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.Trim(parts[1], "\" ")
		catFacet.Term.Include = append(catFacet.Term.Include, val)
		switch parts[0] {
		case "catalog":
			catFacet.Query.BoolTerm.Values = append(catFacet.Query.BoolTerm.Values, val)
		default:
			ctrl.logger.Error().Err(err).Msgf("unknown catalog identifier '%s'", cat.Identifier)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown catalog identifier '%s'", cat.Identifier))
			return
		}
	}
	var size int64 = 1
	var sortField = c.Query("sortField")
	var sortOrder = c.Query("sortOrder")
	var sort = []*client.SortField{}
	if sortField != "" {
		sort = append(sort, &client.SortField{
			Field: sortField,
			Order: sortOrder,
		})
	}
	facets := []*client.InFacet{collFacet}
	if len(catFacet.Query.BoolTerm.Values) > 0 {
		facets = append(facets, catFacet)
	}
	result, err := ctrl.client.Search(c, "", facets, nil, nil, nil, &size, nil, sort)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", "")
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", "", err))
		return
	}

	for _, coll := range ctrl.collections {
		data.Collections[coll.Id] = coll
	}
	for _, cat := range ctrl.catalogs {
		data.Catalogs[cat.Id] = cat
	}

	for _, facet := range result.GetSearch().GetFacets() {
		switch facet.GetName() {
		case "collections":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				colls := data.Collections
				for _, coll := range colls {
					parts := strings.SplitN(coll.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						coll.Count = int(strVal.GetCount())
					}
				}
			}
		case "catalogs":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				cats := data.Catalogs
				for _, cat := range cats {
					parts := strings.SplitN(cat.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						cat.Count = int(strVal.GetCount())
					}
				}
			}
		}
	}

	if err := impressumTemplate.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

func (ctrl *Controller) kontaktPage(c *gin.Context) {
	lang := ctrl.getLang(c)

	templateName := "kontakt.gohtml"
	files := ctrl.getTemplatesByPrefix("kontakt_", "index.gohtml", "impressum.gohtml", "search_grid.gohtml", "detail.gohtml", "zoom.gohtml")
	impressumTemplate, err := ctrl.loadHTMLTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}

	type tplData struct {
		baseData
		Collections map[int64]*CollFacetType `json:"collections"`
		Catalogs    map[int64]*CollFacetType `json:"catalogs"`
	}
	var data = &tplData{
		Collections: map[int64]*CollFacetType{},
		Catalogs:    map[int64]*CollFacetType{},
		baseData:    ctrl.getBaseData(c, lang, "../../"),
	}
	collFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "collections",
			Field:       "category.keyword",
			Size:        200,
			MinDocCount: 0,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "tags.keyword",
				Values: []string{},
				And:    false,
			},
		},
	}
	for _, coll := range ctrl.collections {
		parts := strings.SplitN(coll.Identifier, ":", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.Trim(parts[1], "\" ")
		collFacet.Term.Include = append(collFacet.Term.Include, val)
		switch parts[0] {
		case "cat":
			collFacet.Query.BoolTerm.Values = append(collFacet.Query.BoolTerm.Values, val)
		default:
			ctrl.logger.Error().Err(err).Msgf("unknown collection identifier '%s'", coll.Identifier)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown collection identifier '%s'", coll.Identifier))
			return
		}
	}
	catFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "catalogs",
			Field:       "category.keyword",
			Size:        200,
			MinDocCount: 0,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "tags.keyword",
				Values: []string{},
				And:    false,
			},
		},
	}
	for _, cat := range ctrl.catalogs {
		parts := strings.SplitN(cat.Identifier, ":", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.Trim(parts[1], "\" ")
		catFacet.Term.Include = append(catFacet.Term.Include, val)
		switch parts[0] {
		case "catalog":
			catFacet.Query.BoolTerm.Values = append(catFacet.Query.BoolTerm.Values, val)
		default:
			ctrl.logger.Error().Err(err).Msgf("unknown catalog identifier '%s'", cat.Identifier)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown catalog identifier '%s'", cat.Identifier))
			return
		}
	}
	var size int64 = 1
	var sortField = c.Query("sortField")
	var sortOrder = c.Query("sortOrder")
	var sort = []*client.SortField{}
	if sortField != "" {
		sort = append(sort, &client.SortField{
			Field: sortField,
			Order: sortOrder,
		})
	}
	facets := []*client.InFacet{collFacet}
	if len(catFacet.Query.BoolTerm.Values) > 0 {
		facets = append(facets, catFacet)
	}
	result, err := ctrl.client.Search(c, "", facets, nil, nil, nil, &size, nil, sort)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", "")
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", "", err))
		return
	}

	for _, coll := range ctrl.collections {
		data.Collections[coll.Id] = coll
	}
	for _, cat := range ctrl.catalogs {
		data.Catalogs[cat.Id] = cat
	}

	for _, facet := range result.GetSearch().GetFacets() {
		switch facet.GetName() {
		case "collections":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				colls := data.Collections
				for _, coll := range colls {
					parts := strings.SplitN(coll.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						coll.Count = int(strVal.GetCount())
					}
				}
			}
		case "catalogs":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				cats := data.Catalogs
				for _, cat := range cats {
					parts := strings.SplitN(cat.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						cat.Count = int(strVal.GetCount())
					}
				}
			}
		}
	}

	if err := impressumTemplate.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

func (ctrl *Controller) indexPage(ctx *gin.Context) {
	lang := ctrl.getLang(ctx)

	templateName := "index.gohtml"
	files := ctrl.getTemplatesByPrefix("index_", "impressum.gohtml", "kontakt.gohtml", "search_grid.gohtml", "detail.gohtml", "zoom.gohtml")
	indexTemplate, err := ctrl.loadHTMLTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}

	type tplData struct {
		baseData
		Collections map[int64]*CollFacetType `json:"collections"`
		Catalogs    map[int64]*CollFacetType `json:"catalogs"`
	}
	var data = &tplData{
		Collections: map[int64]*CollFacetType{},
		Catalogs:    map[int64]*CollFacetType{},
		baseData:    ctrl.getBaseData(ctx, lang, ""),
	}

	collFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "collections",
			Field:       "category.keyword",
			Size:        200,
			MinDocCount: 0,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			ExistsTerm: &client.InFilterExistsTerm{
				Field: "signature",
			},
			/*
				BoolTerm: &client.InFilterBoolTerm{
					Field:  "category.keyword",
					Values: []string{},
					And:    false,
				},
			*/
		},
	}
	/*
		for _, coll := range ctrl.collections {
			parts := strings.SplitN(coll.Identifier, ":", 2)
			if len(parts) != 2 {
				continue
			}
			val := strings.Trim(parts[1], "\" ")
			collFacet.Term.Include = append(collFacet.Term.Include, val)
			switch parts[0] {
			case "cat":
				collFacet.Query.BoolTerm.Values = append(collFacet.Query.BoolTerm.Values, val)
			default:
				ctrl.logger.Error().Err(err).Msgf("unknown collection identifier '%s'", coll.Identifier)
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown collection identifier '%s'", coll.Identifier))
				return
			}
		}
	*/
	catFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "catalogs",
			Field:       "catalog.keyword",
			Size:        200,
			MinDocCount: 0,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			ExistsTerm: &client.InFilterExistsTerm{
				Field: "signature",
			},
			/*
				BoolTerm: &client.InFilterBoolTerm{
					Field:  "catalog.keyword",
					Values: []string{},
					And:    false,
				},
			*/
		},
	}
	/*
		for _, cat := range ctrl.catalogs {
			parts := strings.SplitN(cat.Identifier, ":", 2)
			if len(parts) != 2 {
				continue
			}
			val := strings.Trim(parts[1], "\" ")
			catFacet.Term.Include = append(catFacet.Term.Include, val)
			switch parts[0] {
			case "catalog":
				catFacet.Query.BoolTerm.Values = append(catFacet.Query.BoolTerm.Values, val)
			default:
				ctrl.logger.Error().Err(err).Msgf("unknown catalog identifier '%s'", cat.Identifier)
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown catalog identifier '%s'", cat.Identifier))
				return
			}
		}
	*/
	var size int64 = 1
	var sortField = ctx.Query("sortField")
	var sortOrder = ctx.Query("sortOrder")
	var sort = []*client.SortField{}
	if sortField != "" {
		sort = append(sort, &client.SortField{
			Field: sortField,
			Order: sortOrder,
		})
	}
	user := GetUser(ctx)
	filter := []*client.InFilter{
		{
			ExistsTerm: &client.InFilterExistsTerm{
				Field: "poster",
			},
		},
		{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "acl.content.keyword",
				Values: user.Groups,
			},
		},
	}
	facets := []*client.InFacet{
		collFacet,
		catFacet,
	}
	/*
		if collFacet.Query != nil && len(collFacet.Query.BoolTerm.Values) > 0 {
			facets = append(facets, collFacet)
		}
		if catFacet.Query != nil && len(catFacet.Query.BoolTerm.Values) > 0 {
			facets = append(facets, catFacet)
		}
	*/
	result, err := ctrl.client.Search(ctx, "", facets, filter, nil, nil, &size, nil, sort)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", "")
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", "", err))
		return
	}

	//var str string
	for _, coll := range ctrl.collections {
		data.Collections[coll.Id] = coll
	}
	for _, cat := range ctrl.catalogs {
		data.Catalogs[cat.Id] = cat
	}
	//ctrl.logger.Debug().Msg(str)

	for _, facet := range result.GetSearch().GetFacets() {
		switch facet.GetName() {
		case "collections":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				colls := data.Collections
				for _, coll := range colls {
					parts := strings.SplitN(coll.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						coll.Count = int(strVal.GetCount())
					}
				}
			}
		case "catalogs":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				cats := data.Catalogs
				for _, cat := range cats {
					parts := strings.SplitN(cat.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						cat.Count = int(strVal.GetCount())
					}
				}
			}
		}
	}

	if err := indexTemplate.Execute(ctx.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

type queryData struct {
	Search string
}

func (ctrl *Controller) zoomSignature(c *gin.Context) {
	pxs := c.Param("PosX")
	pys := c.Param("PosY")

	posX, err := strconv.Atoi(pxs)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("%s is not a number: %v", pxs, err)
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("%s is not a number: %v", pxs, err))
		return
	}
	posY, err := strconv.Atoi(pys)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("%s is not a number: %v", pys, err)
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("%s is not a number: %v", pys, err))
		return
	}
	var signature string
	for sig, rects := range ctrl.zoomPos {
		for _, rect := range rects {
			if posX >= rect.Min.X && posX <= rect.Max.X {
				if posY >= rect.Min.Y && posY <= rect.Max.Y {
					signature = sig
					break
				}
			}
			if signature != "" {
				break
			}
		}
	}
	c.JSON(http.StatusOK, signature)
}

func (ctrl *Controller) searchPage(c *gin.Context, page string) {
	var lang = c.Param("lang")
	if !ctrl.langAvailable(lang) {
		lang = "de"
	}
	templateName := "search_grid.gohtml"
	files := ctrl.getTemplatesByPrefix("search_", "index.gohtml", "impressum.gohtml", "kontakt.gohtml", "detail.gohtml", "zoom.gohtml")
	gridTemplate, err := ctrl.loadHTMLTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}
	searchString := c.Query("search")
	filterStrings, queryString, err := parseQuery(searchString)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot parse query '%s'", searchString)
		// c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot parse query '%s': %v", searchString, err))
		queryString = searchString
	}

	cursorString := c.Query("cursor")
	ki := c.Request.URL.Query().Has("ki")
	collectionsString := c.Query("collections")
	parts := strings.Split(collectionsString, ",")
	collectionIDs := []int{}
	for _, part := range parts {
		collID, err := strconv.Atoi(part)
		if err != nil || collID == 0 {
			continue
		}
		collectionIDs = append(collectionIDs, collID)
	}
	catalogsString := c.Query("catalogs")
	parts = strings.Split(catalogsString, ",")
	catalogIDs := []int{}
	for _, part := range parts {
		catID, err := strconv.Atoi(part)
		if err != nil || catID == 0 {
			continue
		}
		catalogIDs = append(catalogIDs, catID)
	}
	vocabularyString := c.Query("vocabulary")
	parts = strings.Split(vocabularyString, ",")
	vocabularyIDs := []string{}
	for _, part := range parts {
		vocabularyID := strings.TrimSpace(part)
		if part == "" {
			continue
		}
		vocabularyIDs = append(vocabularyIDs, vocabularyID)
	}
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
	collFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "collections",
			Field:       "category.keyword",
			Size:        200,
			MinDocCount: 0,
			Include:     []string{},
			Exclude:     []string{},
		},
		Query: &client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "category.keyword",
				Values: []string{},
				And:    false,
			},
		},
	}
	for _, coll := range ctrl.collections {
		parts := strings.SplitN(coll.Identifier, ":", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.Trim(parts[1], "\" ")
		collFacet.Term.Include = append(collFacet.Term.Include, val)
		if len(collectionIDs) == 0 || slices.Contains(collectionIDs, int(coll.Id)) {
			switch parts[0] {
			case "cat":
				collFacet.Query.BoolTerm.Values = append(collFacet.Query.BoolTerm.Values, val)
			default:
				ctrl.logger.Error().Err(err).Msgf("unknown collection identifier '%s'", coll.Identifier)
				c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown collection identifier '%s'", coll.Identifier))
				return
			}
		}
	}
	catFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "catalogs",
			Field:       "catalog.keyword",
			Size:        200,
			MinDocCount: 0,
			Include:     []string{},
			Exclude:     []string{},
		},
	}
	if len(catalogIDs) == 0 {
		catFacet.Query = &client.InFilter{
			ExistsTerm: &client.InFilterExistsTerm{
				Field: "signature",
			},
		}
	} else {
		catFacet.Query = &client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "catalog.keyword",
				Values: []string{},
				And:    false,
			},
		}
		for _, cat := range ctrl.catalogs {
			parts := strings.SplitN(cat.Identifier, ":", 2)
			if len(parts) != 2 {
				continue
			}
			val := strings.Trim(parts[1], "\" ")
			catFacet.Term.Include = append(catFacet.Term.Include, val)
			if len(catalogIDs) == 0 || slices.Contains(catalogIDs, int(cat.Id)) {
				switch parts[0] {
				case "catalog":
					catFacet.Query.BoolTerm.Values = append(catFacet.Query.BoolTerm.Values, val)
				default:
					ctrl.logger.Error().Err(err).Msgf("unknown catalog identifier '%s'", cat.Identifier)
					c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown catalog identifier '%s'", cat.Identifier))
					return
				}
			}
		}
	}

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
	var sortField = c.Query("sortField")
	var sortOrder = c.Query("sortOrder")
	var sort = []*client.SortField{}
	if sortField != "" {
		sort = append(sort, &client.SortField{
			Field: sortField,
			Order: sortOrder,
		})
	}
	user := GetUser(c)
	filter := append([]*client.InFilter{}, ctrl.baseFilter...)
	for _, f := range filter {
		if f.BoolTerm != nil {
			if f.BoolTerm.Field == "acl.content.keyword" {
				f.BoolTerm.Values = user.Groups
			}
		}
	}
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
	result, err = ctrl.client.Search(c, queryString, []*client.InFacet{collFacet, catFacet, vocFacet}, filter, nil, nil, nil, &cursorString, sort)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", searchString)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", searchString, err))
		return
	}

	type vocFacetType struct {
		Name    string `json:"name"`
		Count   int    `json:"count"`
		Checked bool   `json:"checked"`
	}

	type collFacetType struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Count   int    `json:"count"`
		Checked bool   `json:"checked"`
	}
	type catFacetType struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Count   int    `json:"count"`
		Checked bool   `json:"checked"`
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
	currentSearchURL := url.Values{}
	if searchString != "" {
		currentSearchURL.Set("search", searchString)
	}
	if collectionsString != "" {
		currentSearchURL.Set("collections", collectionsString)
	}
	if catalogsString != "" {
		currentSearchURL.Set("catalogs", catalogsString)
	}
	if vocabularyString != "" {
		currentSearchURL.Set("vocabulary", vocabularyString)
	}
	var searchParams string
	if len(currentSearchURL) > 0 {
		searchParams = "?" + currentSearchURL.Encode()
	}
	_, isExhibition := c.GetQuery("exhibition")

	data := struct {
		baseData
		//Result           *client.Search_Search      `json:"result"`
		TotalCount       int                        `json:"totalCount"`
		PageInfo         *client.PageInfoFragment   `json:"pageInfo"`
		Edges            []*edge                    `json:"edges"`
		MediaserverBase  string                     `json:"mediaserverBase"`
		RequestQuery     *queryData                 `json:"request"`
		CollectionFacets []*collFacetType           `json:"collectionFacets"`
		CatalogFacets    []*catFacetType            `json:"catalogFacets"`
		VocabularyFacets map[string][]*vocFacetType `json:"vocabularyFacets"`
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
		CollectionFacets: []*collFacetType{},
		CatalogFacets:    []*catFacetType{},
		VocabularyFacets: map[string][]*vocFacetType{},
	}
	if data.baseData.User.IsLoggedIn() {
		data.baseData.DetailAddr = data.baseData.SearchAddr
	}
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
						data.VocabularyFacets[parts[1]] = []*vocFacetType{}
					}
				} else {
					continue
				}
				data.VocabularyFacets[parent] = append(data.VocabularyFacets[parent], &vocFacetType{
					Count:   int(strVal.GetCount()),
					Name:    name,
					Checked: slices.Contains(vocabularyIDs, facetStr),
				})
			}

		case "collections":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				cf := &collFacetType{
					Count: int(strVal.GetCount()),
				}
				for _, coll := range ctrl.collections {
					parts := strings.SplitN(coll.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						cf.ID = int(coll.Id)
						cf.Name = coll.Title
						cf.Checked = slices.Contains(collectionIDs, int(coll.Id))
						data.CollectionFacets = append(data.CollectionFacets, cf)
					}
				}
			}
		case "catalogs":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				cf := &catFacetType{
					Count: int(strVal.GetCount()),
				}
				for _, cat := range ctrl.catalogs {
					parts := strings.SplitN(cat.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						cf.ID = int(cat.Id)
						cf.Name = cat.Title
						cf.Checked = slices.Contains(catalogIDs, int(cat.Id))
						data.CatalogFacets = append(data.CatalogFacets, cf)
					}
				}
			}
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

	if err := gridTemplate.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

func (ctrl *Controller) zoomPage(c *gin.Context) {
	var lang = c.Param("lang")
	if !ctrl.langAvailable(lang) {
		lang = "de"
	}
	templateName := "zoom.gohtml"
	files := ctrl.getTemplatesByPrefix("zoom_", "index.gohtml", "impressum.gohtml", "kontakt.gohtml", "search_grid.gohtml", "detail.gohtml")
	zoomTemplate, err := ctrl.loadHTMLTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}

	_, isExhibition := c.GetQuery("exhibition")
	var data = &struct {
		baseData
	}{
		baseData: baseData{
			Lang:       lang,
			RootPath:   "../",
			Exhibition: isExhibition,
			SearchAddr: ctrl.searchAddr,
			DetailAddr: ctrl.detailAddr,
			Page:       "zoom",
			LoginURL:   ctrl.loginURL,
			Self:       fmt.Sprintf("%s%s", ctrl.externalAddr, c.Request.URL.Path),
			User:       GetUser(c),
			Mode:       ctrl.mode,
		},
	}
	if err := zoomTemplate.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}
