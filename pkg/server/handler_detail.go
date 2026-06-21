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
	"github.com/go-git/go-git/v5/utils/ioutil"
	"github.com/je4/basel-collections/v2/directus"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
	"golang.org/x/text/language"
)

func (ctrl *Controller) detailJSON(c *gin.Context) {
	id := c.Param("signature")
	entry, err := ctrl.getMediathekEntry(c, id)
	if err != nil {
		ctrl.logger.Error().Err(err).Msg("cannot get mediathek entry")
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "missing") {
			status = http.StatusBadRequest
		}
		c.AbortWithStatusJSON(status, err.Error())
		return
	}
	c.JSON(http.StatusOK, entry)
}

func (ctrl *Controller) detailText(c *gin.Context) {
	lang := ctrl.getLang(c)
	templateName := "detail_text.gotmpl"
	id := c.Param("signature")
	entry, err := ctrl.getMediathekEntry(c, id)
	if err != nil {
		ctrl.logger.Error().Err(err).Msg("cannot get mediathek entry")
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "missing") {
			status = http.StatusBadRequest
		}
		c.AbortWithStatusJSON(status, err.Error())
		return
	}

	type tplData struct {
		baseData
		Source          *client.MediathekEntries_MediathekEntries `json:"source"`
		MediaserverBase string                                    `json:"mediaserverBase"`
	}
	var data = &tplData{
		Source:          entry,
		baseData:        ctrl.getBaseData(c, lang, "../"),
		MediaserverBase: ctrl.mediaserverBase,
	}

	files := ctrl.getTemplatesByPrefix("detail_", "index.gohtml", "impressum.gohtml", "kontakt.gohtml", "search_grid.gohtml", "zoom.gohtml")
	tpl, err := ctrl.loadTextTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}
	c.Header("Content-Type", "text/markdown; charset=utf-8")
	if err := tpl.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

func (ctrl *Controller) foliateViewer(c *gin.Context) {
	media := strings.TrimPrefix(c.Query("epub"), "mediaserver:")
	if media == "" {
		ctrl.logger.Error().Msgf("epub parameter missing")
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("epub parameter missing"))
		return
	}
	type tplData struct {
		RootPath string `json:"rootPath"`
		Media    string `json:"media"`
	}
	mediaUrl, _ := url.JoinPath(ctrl.mediaserverBase, media, "master")
	var data = &tplData{
		RootPath: "../",
		Media:    mediaUrl,
	}
	templateName := "foliatejsviewer.gohtml"
	files := ctrl.getTemplatesByPrefix("foliatejs", "index.gohtml", "impressum.gohtml", "kontakt.gohtml", "search_grid.gohtml", "detail.gohtml", "zoom.gohtml")
	tpl, err := ctrl.loadHTMLTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}
	if err := tpl.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

func (ctrl *Controller) detail(c *gin.Context) {
	lang := ctrl.getLang(c)
	sourceString := c.Query("source")
	searchString := c.Query("search")
	cursorString := c.Query("cursor")
	collectionsString := c.Query("collections")
	catalogString := c.Query("catalogs")
	vocabularyString := c.Query("vocabulary")
	ki := c.Request.URL.Query().Has("ki")
	query := url.Values{}
	if searchString != "" {
		query.Set("search", searchString)
	}
	if collectionsString != "" {
		query.Set("collections", collectionsString)
	}
	if catalogString != "" {
		query.Set("catalogs", catalogString)
	}
	if vocabularyString != "" {
		query.Set("vocabulary", vocabularyString)
	}
	if sourceString != "" {
		query.Set("source", sourceString)
	}
	if cursorString != "" {
		query.Set("cursor", cursorString)
	}
	if vocabularyString != "" {
		query.Set("vocabulary", vocabularyString)
	}
	if ki {
		query.Set("ki", "")

	}
	templateName := "detail.gohtml"
	files := ctrl.getTemplatesByPrefix("detail_", "index.gohtml", "impressum.gohtml", "kontakt.gohtml", "search_grid.gohtml", "zoom.gohtml")
	textTemplate, err := ctrl.loadHTMLTemplate(templateName, files)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}
	id := c.Param("signature")
	entry, err := ctrl.getMediathekEntry(c, id)
	if err != nil {
		ctrl.logger.Error().Err(err).Msg("cannot get mediathek entry")
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "missing") {
			status = http.StatusBadRequest
		}
		c.AbortWithStatusJSON(status, err.Error())
		return
	}

	type tplData struct {
		baseData
		IFrame          bool
		Source          *client.MediathekEntries_MediathekEntries `json:"source"`
		MediaserverBase string                                    `json:"mediaserverBase"`
		SearchSource    string                                    `json:"searchSource"`
		//ShowContent      bool
		//ProtectedContent bool
	}
	var searchParams string
	if len(query) > 0 {
		searchParams = "?" + query.Encode()
	}
	_, isIFrame := c.GetQuery("iframe")
	_, isExhibition := c.GetQuery("exhibition")
	categories := entry.GetBase().GetCategory()
	slices.SortFunc(categories, func(a, b string) int {
		return len(b) - len(a)
	})
	var newCategories = []string{}
	for _, cat := range categories {
		isPrefix := false
		for _, newCat := range newCategories {
			if strings.HasPrefix(newCat, cat) {
				isPrefix = true
				break
			}
		}
		if !isPrefix {
			newCategories = append(newCategories, cat)
		}
	}
	entry.Base.Category = newCategories
	bd := ctrl.getBaseData(c, lang, "../../")
	bd.Exhibition = isExhibition
	bd.Params = template.URL(strings.TrimPrefix(searchParams, "?"))

	var data = &tplData{
		Source:          entry,
		IFrame:          isIFrame,
		SearchSource:    sourceString,
		baseData:        bd,
		MediaserverBase: ctrl.mediaserverBase,
	}

	if err := textTemplate.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

func (ctrl *Controller) qr(c *gin.Context) {
	url := c.Query("url")
	qrc, err := qrcode.New(url)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot create qrcode for '%s'", url)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot create qrcode for '%s': %v", url, err))
		return
	}
	w := standard.NewWithWriter(ioutil.WriteNopCloser(c.Writer), standard.WithBgTransparent())
	if err := qrc.Save(w); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot save qrcode for '%s'", url)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot save qrcode for '%s': %v", url, err))
		return
	}
}

func (ctrl *Controller) detailTextList(c *gin.Context) {
	var collectionStr = c.Param("collection")
	collectionId, err := strconv.Atoi(collectionStr)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot convert collection '%s' to int", collectionStr)
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("cannot convert collection '%s' to int: %v", collectionStr, err))
		return
	}
	colls, err := ctrl.dir.GetCollections()
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot get collections")
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot get collections: %v", err))
		return
	}
	var theColl *directus.Collection
	for _, coll := range colls {
		if coll.Id == int64(collectionId) {
			theColl = coll
			break
		}
	}
	if theColl == nil {
		ctrl.logger.Error().Err(err).Msgf("collection '%s' not found", collectionStr)
		c.AbortWithStatusJSON(http.StatusNotFound, fmt.Sprintf("collection '%s' not found", collectionStr))
		return
	}
	parts := strings.SplitN(theColl.Identifier, ":", 2)
	if len(parts) != 2 {
		ctrl.logger.Error().Err(err).Msgf("unknown collection identifier '%s'", theColl.Identifier)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown collection identifier '%s'", theColl.Identifier))
		return
	}
	if parts[0] != "cat" {
		ctrl.logger.Error().Err(err).Msgf("collection identifier not cat '%s'", theColl.Identifier)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("collection identifier not cat '%s'", theColl.Identifier))
		return
	}
	var cursorString string
	cVal := strings.Trim(parts[1], "\" ")
	var langs = []language.Tag{language.German, language.English, language.French, language.Italian}
	var languageNamerEN = languageNamer["en"]
	var sortField = c.Query("sortField")
	var sortOrder = c.Query("sortOrder")
	var sort = []*client.SortField{}
	if sortField != "" {
		sort = append(sort, &client.SortField{
			Field: sortField,
			Order: sortOrder,
		})
	}
	c.Header("Content-Type", "text/plain; charset=utf-8")
	for {
		result, err := ctrl.client.Search(
			c,
			"",
			[]*client.InFacet{},
			[]*client.InFilter{
				&client.InFilter{
					BoolTerm: &client.InFilterBoolTerm{
						Field:  "category.keyword",
						And:    false,
						Values: []string{cVal},
					},
				},
			},
			nil,
			nil,
			nil,
			&cursorString,
			sort,
		)
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot search for collection '%s'", collectionStr)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for collection '%s': %v", collectionStr, err))
			return
		}
		for _, edge := range result.GetSearch().GetEdges() {
			for _, lang := range langs {
				_, _ = c.Writer.WriteString(fmt.Sprintf("%s/detailtext/%s/%s %s (Document %s)\n", ctrl.externalAddr, edge.Base.Signature, lang.String(), languageNamerEN.Name(lang), edge.Base.Signature))
			}
		}
		if !result.GetSearch().GetPageInfo().GetHasNextPage() {
			break
		}
		cursorString = result.GetSearch().GetPageInfo().GetEndCursor()
	}
}
