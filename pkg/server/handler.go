package server

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/je4/revcat/v2/tools/client"
	"gopkg.in/yaml.v3"
)

// impressumPage renders the imprint page.
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
	result, err := ctrl.client.Search(c, "all", "", facets, nil, nil, nil, &size, nil, sort)
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

var frontMatterRegex = regexp.MustCompile(`(?s)^[ \t]*-{3,}[ \t]*\r?\n.*?\r?\n[ \t]*-{3,}[ \t]*\r?\n?`)

func (ctrl *Controller) parseMetadata(mdData []byte, name string) map[string]string {
	var meta = map[string]string{}
	if loc := frontMatterRegex.FindIndex(mdData); loc != nil {
		frontMatter := mdData[loc[0]:loc[1]]

		// remove separators
		// find first newline (ends first separator)
		firstNL := bytes.IndexByte(frontMatter, '\n')
		// find last occurrence of dashes
		lastDashes := bytes.LastIndex(frontMatter, []byte("---"))
		if firstNL != -1 && lastDashes != -1 && firstNL < lastDashes {
			yamlBytes := frontMatter[firstNL+1 : lastDashes]
			if err := yaml.Unmarshal(yamlBytes, &meta); err != nil {
				ctrl.logger.Error().Err(err).Msgf("cannot decode metadata in %s", name)
			}
			for k, v := range meta {
				lk := strings.ToLower(k)
				if lk != k {
					delete(meta, k)
					meta[lk] = v
				}
			}
		}
	}
	if meta["title"] == "" {
		meta["title"] = name
	}
	return meta
}

// pagePage renders the page page.
func (ctrl *Controller) pagePage(c *gin.Context) {
	lang := ctrl.getLang(c)

	name := strings.TrimPrefix(c.Param("any"), "/")
	if strings.HasSuffix(name, "/") {
		name = path.Join(name, "readme.md")
	}
	if strings.ToLower(path.Ext(name)) != ".md" {
		c.FileFromFS(name, http.FS(ctrl.pagesFS))
		return
	}
	mdData, err := fs.ReadFile(ctrl.pagesFS, name)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot open %s: %v", name, err))
		return
	}
	// check for metadata prefix
	meta := ctrl.parseMetadata(mdData, name)

	htmlBuffer := bytes.NewBuffer(nil)
	if err := ctrl.md.Convert(mdData, htmlBuffer); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot render %s: %v", name, err))
		return
	}

	templateName := "page.gohtml"
	files := ctrl.getTemplatesByPrefix("page_", "index.gohtml", "impressum.gohtml", "search_grid.gohtml", "detail.gohtml", "zoom.gohtml")
	pageTemplate, err := ctrl.loadHTMLTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}

	type tplData struct {
		baseData
		HTML string
		Meta map[string]string
	}
	var data = &tplData{
		baseData: ctrl.getBaseData(c, lang, "../../"),
		HTML:     htmlBuffer.String(),
		Meta:     meta,
	}

	if err := pageTemplate.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

// kontaktPage renders the contact page.
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
	result, err := ctrl.client.Search(c, "all", "", facets, nil, nil, nil, &size, nil, sort)
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

// indexPage renders the home page.
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
		Medias      map[int64]*CollFacetType
	}
	var data = &tplData{
		Collections: map[int64]*CollFacetType{},
		Catalogs:    map[int64]*CollFacetType{},
		Medias:      map[int64]*CollFacetType{},
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
		switch parts[0] {
		case "cat":
			collFacet.Query.BoolTerm.Values = append(collFacet.Query.BoolTerm.Values, val)
		default:
			ctrl.logger.Error().Err(err).Msgf("unknown collection identifier '%s'", coll.Identifier)
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown collection identifier '%s'", coll.Identifier))
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
	mediaFacet := &client.InFacet{
		Term: &client.InFacetTerm{
			Name:        "mediatypes",
			Field:       "mediatype.keyword",
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
	filter := append([]*client.InFilter{}, ctrl.baseFilter...)
	/*
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
	*/
	facets := []*client.InFacet{
		collFacet,
		catFacet,
		mediaFacet,
	}
	/*
		if collFacet.Query != nil && len(collFacet.Query.BoolTerm.Values) > 0 {
			facets = append(facets, collFacet)
		}
		if catFacet.Query != nil && len(catFacet.Query.BoolTerm.Values) > 0 {
			facets = append(facets, catFacet)
		}
	*/
	result, err := ctrl.client.Search(ctx, "all", "", facets, filter, nil, nil, &size, nil, sort)
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
	for _, media := range ctrl.medias {
		data.Medias[media.Id] = media
	}
	//ctrl.logger.Debug().Msg(str)

	for _, facet := range result.GetSearch().GetFacets() {
		facetName := facet.GetName()
		switch facetName {
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
		case "mediatypes":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				medias := data.Medias
				for _, media := range medias {
					parts := strings.SplitN(media.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						media.Count = int(strVal.GetCount())
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

// zoomSignature handles the request for a signature based on coordinates in the zoom view.
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

// zoomPage renders the zoom view page.
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
