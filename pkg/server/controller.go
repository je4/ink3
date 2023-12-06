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
	"html/template"
	"io/fs"
	"net/http"
	"slices"
)

func (ctrl *Controller) funcMap() template.FuncMap {
	fm := sprig.FuncMap()

	fm["toHTML"] = func(s string) template.HTML {
		return template.HTML(s)
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
	}
	router := gin.Default()
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	router.Use(cors.New(corsConfig))
	router.StaticFS("/static", http.FS(ctrl.staticFS))

	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/de")
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
	if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
		lang = "de"
	}

	templateName := "index.gohtml"
	tmpl, ok := ctrl.templateCache[templateName]
	if !ok {
		var err error
		tmpl, err = template.New(templateName).Funcs(ctrl.funcMap()).ParseFS(ctrl.templateFS, templateName)
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot parse template '%s'", templateName)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot parse template '%s': %v", templateName, err))
			return
		}
		if !ctrl.templateDebug {
			ctrl.templateCache[templateName] = tmpl
		}
	}

	cat, err := ctrl.dir.GetCatalogue(ctrl.catalogID)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot get catalogue #%v", ctrl.catalogID)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot get catalogue #%v: %v", ctrl.catalogID, err))
		return
	}

	type tplData struct {
		Collections []*directus.Collection `json:"collections"`
		Lang        string
	}
	var data = &tplData{
		Collections: []*directus.Collection{},
		Lang:        lang,
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

func (ctrl *Controller) searchGridPage(c *gin.Context) {
	var lang = c.Param("lang")
	if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
		lang = "de"
	}
	templateName := "search_grid.gohtml"
	tmpl, ok := ctrl.templateCache[templateName]
	if !ok {
		var err error
		tmpl, err = template.New(templateName).Funcs(ctrl.funcMap()).ParseFS(ctrl.templateFS, templateName)
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot parse template '%s'", templateName)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot parse template '%s': %v", templateName, err))
			return
		}
		if !ctrl.templateDebug {
			ctrl.templateCache[templateName] = tmpl
		}
	}
	searchString := c.Query("search")
	result, err := ctrl.client.Search(context.Background(), searchString, []*client.FacetInput{}, []*client.FilterInput{}, nil, nil, nil, nil)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", searchString)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", searchString, err))
		return
	}
	data := struct {
		Result          client.Search_Search `json:"result"`
		MediaserverBase string               `json:"mediaserverBase"`
		Lang            string               `json:"lang"`
	}{
		Result:          result.Search,
		MediaserverBase: ctrl.mediaserverBase,
		Lang:            lang,
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
