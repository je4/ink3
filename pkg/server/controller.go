package server

import (
	"context"
	"crypto/tls"
	"emperror.dev/errors"
	"fmt"
	"github.com/Masterminds/sprig/v3"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gosimple/slug"
	"github.com/je4/basel-collections/v2/directus"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/utils/v2/pkg/zLogger"
	"github.com/je4/zsearch/v2/pkg/translate"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	tmpl "text/template"
)

type baseData struct {
	Lang     string
	RootPath string
	Params   template.URL
}

func (ctrl *Controller) funcMap() template.FuncMap {
	fm := sprig.FuncMap()

	fm["toHTML"] = func(s string) template.HTML {
		return template.HTML(s)
	}
	fm["toURL"] = func(s string) template.URL {
		return template.URL(s)
	}
	fm["toJS"] = func(s string) template.JS {
		return template.JS(s)
	}
	fm["toJSStr"] = func(s string) template.JSStr {
		return template.JSStr(s)
	}
	fm["localize"] = func(key, lang string) string {
		localizer := i18n.NewLocalizer(ctrl.bundle, lang)

		result, err := localizer.LocalizeMessage(&i18n.Message{
			ID: key,
		})
		if err != nil {
			result = fmt.Sprintf("cannot localize '%s': %v", key, err)
		}
		return result // fmt.Sprintf("%s (%s)", result, lang)
	}
	fm["slug"] = func(s string, lang string) string {
		return strings.Replace(slug.MakeLang(s, lang), "-", "_", -1)
	}

	type size struct {
		Width  int64 `json:"width"`
		Height int64 `json:"height"`
	}
	fm["calcAspectSize"] = func(width, height, maxWidth, maxHeight int64) size {
		aspect := float64(width) / float64(height)
		maxAspect := float64(maxWidth) / float64(maxHeight)
		if aspect > maxAspect {
			return size{
				Width:  maxWidth,
				Height: int64(float64(maxWidth) / aspect),
			}
		} else {
			return size{
				Width:  int64(float64(maxHeight) * aspect),
				Height: maxHeight,
			}
		}
	}
	fm["multiLang"] = func(mf []*client.MultiLangFragment) *translate.MultiLangString {
		m := &translate.MultiLangString{}
		for _, f := range mf {
			lang, _ := language.Parse(f.Lang)
			m.Set(f.Value, lang, f.Translated)
		}
		return m
	}
	return fm
}

func NewController(localAddr, externalAddr string, cert *tls.Certificate, templateFS, staticFS fs.FS, dir *directus.Directus, client client.RevCatGraphQLClient, catalogID int, mediaserverBase string, bundle *i18n.Bundle, templateDebug bool, logger zLogger.ZLogger) *Controller {

	ctrl := &Controller{
		localAddr:       localAddr,
		externalAddr:    externalAddr,
		srv:             nil,
		cert:            cert,
		templateFS:      templateFS,
		staticFS:        staticFS,
		templateDebug:   templateDebug,
		templateCache:   make(map[string]any),
		logger:          logger,
		dir:             dir,
		catalogID:       int64(catalogID),
		client:          client,
		mediaserverBase: mediaserverBase,
		bundle:          bundle,
		languageMatcher: language.NewMatcher([]language.Tag{
			language.English, // The first language is used as fallback.
			language.German,
			language.French,
			language.Italian,
		}),
	}
	router := gin.Default()
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	router.Use(cors.New(corsConfig))
	router.StaticFS("/static", http.FS(ctrl.staticFS))

	router.GET("/", func(c *gin.Context) {
		cookieLang, _ := c.Request.Cookie("lang")
		accept := c.Request.Header.Get("Accept-Language")
		langTag, _ := language.MatchStrings(ctrl.languageMatcher, cookieLang.String(), accept)
		langBase, _ := langTag.Base()
		lang := langBase.String()
		if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
			lang = "en"
		}
		c.Redirect(http.StatusTemporaryRedirect, "/"+lang)
	})

	router.GET("/:lang", func(c *gin.Context) {
		ctrl.indexPage(c)
	})

	router.GET("/search", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/search/de")
	})

	router.POST("/search/:lang", func(c *gin.Context) {
		ctrl.searchGridPage(c)
	})

	router.GET("/search/:lang", func(c *gin.Context) {
		ctrl.searchGridPage(c)
	})

	router.GET("/detailtext/:signature/:lang", func(c *gin.Context) {
		ctrl.detailText(c)
	})

	router.GET("/detail/:lang", func(c *gin.Context) {
		ctrl.detail(c)
	})

	var tlsConfig *tls.Config
	if ctrl.cert != nil {
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{*ctrl.cert},
		}
	}
	ctrl.srv = &http.Server{
		Addr:      ctrl.localAddr,
		Handler:   router,
		TLSConfig: tlsConfig,
	}
	return ctrl
}

type Controller struct {
	localAddr       string
	externalAddr    string
	srv             *http.Server
	cert            *tls.Certificate
	logger          zLogger.ZLogger
	templateFS      fs.FS
	staticFS        fs.FS
	dir             *directus.Directus
	templateDebug   bool
	templateCache   map[string]any
	templateMutex   sync.Mutex
	catalogID       int64
	client          client.RevCatGraphQLClient
	mediaserverBase string
	bundle          *i18n.Bundle
	languageMatcher language.Matcher
}

func (ctrl *Controller) Start() error {
	go func() {
		if ctrl.srv.TLSConfig == nil {
			fmt.Printf("starting server at http://%s\n", ctrl.localAddr)
			if err := ctrl.srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				// unexpected error. port in use?
				fmt.Errorf("server on '%s' ended: %v", ctrl.localAddr, err)
			}
		} else {
			fmt.Printf("starting server at https://%s\n", ctrl.localAddr)
			if err := ctrl.srv.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
				// unexpected error. port in use?
				fmt.Errorf("server on '%s' ended: %v", ctrl.localAddr, err)
			}
		}
		// always returns error. ErrServerClosed on graceful close
	}()

	return nil
}

func (ctrl *Controller) Stop() error {
	return ctrl.srv.Shutdown(context.Background())
}

func (ctrl *Controller) indexPage(c *gin.Context) {
	var lang = c.Param("lang")
	if lang == "" {
		lang = "de"
	}
	if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
		lang = "de"
	}

	templateName := "index.gohtml"
	tmpl, err := ctrl.loadHTMLTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}

	cat, err := ctrl.dir.GetCatalogue(ctrl.catalogID)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot get catalogue #%v", ctrl.catalogID)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot get catalogue #%v: %v", ctrl.catalogID, err))
		return
	}

	type tplData struct {
		baseData
		Collections []*directus.Collection `json:"collections"`
	}
	var data = &tplData{
		Collections: []*directus.Collection{},
		baseData: baseData{
			Lang:     lang,
			RootPath: "",
		},
	}
	for _, collid := range cat.Collections {
		coll, err := ctrl.dir.GetCollection(collid.CollectionID.Id)
		if err != nil {
			continue
			/*
				ctrl.logger.Error().Err(err).Msgf("cannot get collection #%v", collid.CollectionID.Id)
				c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot get collection #%v: %v", collid.CollectionID.Id, err))
				return
			*/
		}
		if coll.Status != "published" {
			continue
		}
		data.Collections = append(data.Collections, coll)
	}

	if err := tmpl.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

func (ctrl *Controller) loadHTMLTemplate(name string, files []string) (*template.Template, error) {
	if strings.ToLower(filepath.Ext(name)) != ".gohtml" {
		return nil, errors.Errorf("template '%s' has wrong extension (should be .gohtml)", name)
	}
	ctrl.templateMutex.Lock()
	defer ctrl.templateMutex.Unlock()
	tpl, ok := ctrl.templateCache[name]
	if !ok {
		var err error
		tpl, err = template.New(name).Funcs(ctrl.funcMap()).ParseFS(ctrl.templateFS, files...)
		if err != nil {
			return nil, errors.Wrapf(err, "cannot parse template '%s'", name)
		}
		if !ctrl.templateDebug {
			ctrl.templateCache[name] = tpl
		}
	}
	return tpl.(*template.Template), nil
}

func (ctrl *Controller) loadTextTemplate(name string, files []string) (*tmpl.Template, error) {
	if strings.ToLower(filepath.Ext(name)) != ".gotmpl" {
		return nil, errors.Errorf("template '%s' has wrong extension (should be .gotmpl)", name)
	}
	ctrl.templateMutex.Lock()
	defer ctrl.templateMutex.Unlock()
	tpl, ok := ctrl.templateCache[name]
	if !ok {
		var err error
		tpl, err = tmpl.New(name).Funcs(ctrl.funcMap()).ParseFS(ctrl.templateFS, files...)
		if err != nil {
			return nil, errors.Wrapf(err, "cannot parse template '%s'", name)
		}
		if !ctrl.templateDebug {
			ctrl.templateCache[name] = tpl
		}
	}
	return tpl.(*tmpl.Template), nil
}

type queryData struct {
	Search string
}

func (ctrl *Controller) searchGridPage(c *gin.Context) {
	var lang = c.Param("lang")
	if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
		lang = "de"
	}
	templateName := "search_grid.gohtml"
	tmpl, err := ctrl.loadHTMLTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}
	searchString := c.Query("search")
	afterString := c.Query("after")
	beforeString := c.Query("before")
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
	cat, err := ctrl.dir.GetCatalogue(ctrl.catalogID)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot get catalogue #%v", ctrl.catalogID)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot get catalogue #%v: %v", ctrl.catalogID, err))
		return
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
			MinDocCount: 0,
			Include:     []string{"voc:.*"},
			Exclude:     []string{},
		},
		Query: client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "tags.keyword",
				Values: vocabularyIDs,
				And:    false,
			},
		},
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
		Query: client.InFilter{
			BoolTerm: &client.InFilterBoolTerm{
				Field:  "category.keyword",
				Values: []string{},
				And:    false,
			},
		},
	}
	for _, collid := range cat.Collections {
		coll, err := ctrl.dir.GetCollection(collid.CollectionID.Id)
		if err != nil {
			continue
		}
		if coll.Status != "published" {
			continue
		}
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

	result, err := ctrl.client.Search(context.Background(), searchString, []*client.InFacet{collFacet, vocFacet}, []*client.InFilter{}, nil, &afterString, nil, &beforeString)
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
	data := struct {
		baseData
		Result           *client.Search_Search      `json:"result"`
		MediaserverBase  string                     `json:"mediaserverBase"`
		RequestQuery     *queryData                 `json:"request"`
		CollectionFacets []*collFacetType           `json:"collectionFacets"`
		VocabularyFacets map[string][]*vocFacetType `json:"vocabularyFacets"`
	}{
		Result:          result.GetSearch(),
		MediaserverBase: ctrl.mediaserverBase,
		baseData: baseData{
			Lang:     lang,
			Params:   template.URL(c.Request.URL.Query().Encode()),
			RootPath: "../",
		},
		RequestQuery: &queryData{
			Search: searchString,
		},
		CollectionFacets: []*collFacetType{},
		VocabularyFacets: map[string][]*vocFacetType{},
	}
	for _, facet := range data.Result.GetFacets() {
		switch facet.GetName() {
		case "vocabulary":
			for _, val := range facet.GetValues() {
				strVal := val.GetFacetValueString()
				if strVal == nil {
					continue
				}
				facetStr := strVal.GetStrVal()
				parts := strings.Split(facetStr, ":")
				if len(parts) != 3 {
					continue
				}
				if val.GetFacetValueInt() == nil || val.GetFacetValueInt().GetIntVal() == 0 {
					if !strings.HasPrefix(parts[1], "voc_") {
						continue
					}
				}
				parent := parts[1] // slug.MakeLang(parts[1], "de")
				if _, ok := data.VocabularyFacets[parent]; !ok {
					data.VocabularyFacets[parts[1]] = []*vocFacetType{}
				}
				data.VocabularyFacets[parent] = append(data.VocabularyFacets[parent], &vocFacetType{
					Count:   int(strVal.GetCount()),
					Name:    parts[2],
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
				colls, err := ctrl.dir.GetCollections()
				if err != nil {
					ctrl.logger.Error().Err(err).Msgf("cannot get collections")
					c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot get collections: %v", err))
					return
				}
				for _, coll := range colls {
					parts := strings.SplitN(coll.Identifier, ":", 2)
					if len(parts) != 2 {
						continue
					}
					cVal := strings.Trim(parts[1], "\" ")
					if cVal == facetStr {
						cf.ID = int(coll.Id)
						cf.Name = coll.GetTitle()
						cf.Checked = slices.Contains(collectionIDs, int(coll.Id))
						data.CollectionFacets = append(data.CollectionFacets, cf)
					}
				}
			}
		}
	}

	if err := tmpl.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

var textTemplate *tmpl.Template

func (ctrl *Controller) detailText(c *gin.Context) {
	var lang = c.Param("lang")
	if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
		lang = "de"
	}
	templateName := fmt.Sprintf("detail_text.%s.gotmpl", lang)
	id := c.Param("signature")
	if id == "" {
		ctrl.logger.Error().Msgf("id missing")
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("id missing"))
		return
	}

	source, err := ctrl.client.MediathekEntries(context.Background(), []string{id})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot get source '%s'", id)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot get source '%s': %v", id, err))
		return
	}
	if source == nil || len(source.MediathekEntries) == 0 {
		ctrl.logger.Error().Err(err).Msgf("source '%s' not found", id)
		c.AbortWithStatusJSON(http.StatusNotFound, fmt.Sprintf("source '%s' not found", id))
		return
	}

	type tplData struct {
		baseData
		Source          *client.MediathekEntries_MediathekEntries `json:"source"`
		MediaserverBase string                                    `json:"mediaserverBase"`
	}
	var data = &tplData{
		Source: source.MediathekEntries[0],
		baseData: baseData{
			Lang:     lang,
			RootPath: "../",
		},
		MediaserverBase: ctrl.mediaserverBase,
	}

	tpl, err := ctrl.loadTextTemplate(templateName, []string{templateName})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}
	c.Set("Content-Type", "text/markdown; charset=utf-8")
	if err := tpl.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}

func (ctrl *Controller) detail(c *gin.Context) {
	var lang = c.Param("lang")
	if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
		lang = "de"
	}
	templateName := "detail.gohtml"
	tmpl, err := ctrl.loadHTMLTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}
	id := c.Param("id")
	if id == "" {
		ctrl.logger.Error().Err(err).Msgf("id missing")
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("id missing"))
		return
	}

	source, err := ctrl.client.MediathekEntries(context.Background(), []string{id})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot get source '%s'", id)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot get source '%s': %v", id, err))
		return
	}
	if source == nil || len(source.MediathekEntries) == 0 {
		ctrl.logger.Error().Err(err).Msgf("source '%s' not found", id)
		c.AbortWithStatusJSON(http.StatusNotFound, fmt.Sprintf("source '%s' not found", id))
		return
	}

	type tplData struct {
		baseData
		Source          *client.MediathekEntries_MediathekEntries `json:"source"`
		MediaserverBase string                                    `json:"mediaserverBase"`
	}
	var data = &tplData{
		Source: source.MediathekEntries[0],
		baseData: baseData{
			Lang:     lang,
			RootPath: "../",
		},
		MediaserverBase: ctrl.mediaserverBase,
	}

	if err := tmpl.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}
