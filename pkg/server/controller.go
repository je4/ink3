package server

import (
	"context"
	"crypto/tls"
	"emperror.dev/errors"
	"fmt"
	"github.com/Masterminds/sprig/v3"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/je4/basel-collections/v2/directus"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/utils/v2/pkg/zLogger"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"html/template"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"
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
		return result
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
		templateCache:   make(map[string]*template.Template),
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
	templateCache   map[string]*template.Template
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
	tmpl, err := ctrl.loadTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
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
func (ctrl *Controller) loadTemplate(name string, files []string) (*template.Template, error) {
	tmpl, ok := ctrl.templateCache[name]
	if !ok {
		var err error
		tmpl, err = template.New(name).Funcs(ctrl.funcMap()).ParseFS(ctrl.templateFS, files...)
		if err != nil {
			return nil, errors.Wrapf(err, "cannot parse template '%s'", name)
		}
		if !ctrl.templateDebug {
			ctrl.templateCache[name] = tmpl
		}
	}
	return tmpl, nil
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
	tmpl, err := ctrl.loadTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}
	searchString := c.Query("search")
	afterString := c.Query("after")
	beforeString := c.Query("before")
	collections := []int{}
	for key, q := range c.Request.URL.Query() {
		key = strings.ToLower(key)
		if !strings.HasPrefix(key, "collection_") {
			continue
		}
		if len(q) == 0 || strings.ToLower(q[0]) != "on" {
			continue
		}
		collectionID, err := strconv.Atoi(key[11:])
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot convert collection id '%s' to int", key[11:])
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot convert collection id '%s' to int: %v", key[11:], err))
			return
		}
		collections = append(collections, collectionID)
	}
	cat, err := ctrl.dir.GetCatalogue(ctrl.catalogID)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot get catalogue #%v", ctrl.catalogID)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot get catalogue #%v: %v", ctrl.catalogID, err))
		return
	}
	var and bool = false
	collFilter := &client.FilterInput{
		Field:        "category.keyword",
		And:          &and,
		ValuesString: []string{},
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
		if len(collections) == 0 || slices.Contains(collections, int(coll.Id)) {
			parts := strings.SplitN(coll.Identifier, ":", 2)
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "cat":
				collFilter.ValuesString = append(collFilter.ValuesString, strings.Trim(parts[1], "\""))
			default:
				ctrl.logger.Error().Err(err).Msgf("unknown collection identifier '%s'", coll.Identifier)
				c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown collection identifier '%s'", coll.Identifier))
				return
			}
		}
	}

	result, err := ctrl.client.Search(context.Background(), searchString, []*client.FacetInput{}, []*client.FilterInput{collFilter}, nil, &afterString, nil, &beforeString)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", searchString)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", searchString, err))
		return
	}
	data := struct {
		baseData
		Result          client.Search_Search `json:"result"`
		MediaserverBase string               `json:"mediaserverBase"`
		RequestQuery    *queryData           `json:"request"`
	}{
		Result:          result.Search,
		MediaserverBase: ctrl.mediaserverBase,
		baseData: baseData{
			Lang:     lang,
			Params:   template.URL(c.Request.URL.Query().Encode()),
			RootPath: "../",
		},
		RequestQuery: &queryData{
			Search: searchString,
		},
	}
	/*
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot marshal result")
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot marshal result: %v", err))
			return
		}
		print(string(b))
	*/
	if err := tmpl.Execute(c.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}
