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
	"github.com/je4/utils/v2/pkg/zLogger"
	"html/template"
	"io/fs"
	"net/http"
)

func funcMap() template.FuncMap {
	fm := sprig.FuncMap()
	fm["toHTML"] = func(s string) template.HTML {
		return template.HTML(s)
	}
	return fm
}

func NewController(localAddr, externalAddr string, cert *tls.Certificate, templateFS, staticFS fs.FS, dir *directus.Directus, catalogID int, templateDebug bool, logger zLogger.ZLogger) *Controller {

	ctrl := &Controller{
		localAddr:     localAddr,
		externalAddr:  externalAddr,
		srv:           nil,
		cert:          cert,
		templateFS:    templateFS,
		staticFS:      staticFS,
		templateDebug: templateDebug,
		templateCache: make(map[string]*template.Template),
		logger:        logger,
		dir:           dir,
		catalogID:     int64(catalogID),
	}
	router := gin.Default()
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	router.Use(cors.New(corsConfig))

	router.StaticFS("/static", http.FS(ctrl.staticFS))

	router.GET("/", func(c *gin.Context) {
		ctrl.indexPage(c)
	})

	router.POST("/search", func(c *gin.Context) {
		ctrl.searchGridPage(c)
	})
	router.GET("/search", func(c *gin.Context) {
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
	localAddr     string
	externalAddr  string
	srv           *http.Server
	cert          *tls.Certificate
	logger        zLogger.ZLogger
	templateFS    fs.FS
	staticFS      fs.FS
	dir           *directus.Directus
	templateDebug bool
	templateCache map[string]*template.Template
	catalogID     int64
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
	templateName := "index.gohtml"
	tmpl, ok := ctrl.templateCache[templateName]
	if !ok {
		var err error
		tmpl, err = template.New(templateName).Funcs(funcMap()).ParseFS(ctrl.templateFS, templateName)
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
	}
	var data = &tplData{
		Collections: []*directus.Collection{},
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
	templateName := "search_grid.gohtml"
	tmpl, ok := ctrl.templateCache[templateName]
	if !ok {
		var err error
		tmpl, err = template.New(templateName).Funcs(funcMap()).ParseFS(ctrl.templateFS, templateName)
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot parse template '%s'", templateName)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot parse template '%s': %v", templateName, err))
			return
		}
		if !ctrl.templateDebug {
			ctrl.templateCache[templateName] = tmpl
		}
	}
	if err := tmpl.Execute(c.Writer, nil); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
		return
	}
}
