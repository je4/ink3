package server

import (
	"context"
	"crypto/tls"
	"emperror.dev/errors"
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/je4/utils/v2/pkg/zLogger"
	"html/template"
	"io/fs"
	"net/http"
)

func NewController(localAddr, externalAddr string, cert *tls.Certificate, templateFS, staticFS fs.FS, templateDebug bool, logger zLogger.ZLogger) *Controller {

	ctrl := &Controller{
		localAddr:     localAddr,
		externalAddr:  externalAddr,
		srv:           nil,
		cert:          cert,
		templateFS:    templateFS,
		staticFS:      staticFS,
		templateDebug: templateDebug,
		logger:        logger,
	}
	router := gin.Default()
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	router.Use(cors.New(corsConfig))

	router.StaticFS("/static", http.FS(ctrl.staticFS))

	router.GET("/", func(c *gin.Context) {
		ctrl.indexPage(c)
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
	templateDebug bool
	templateCache map[string]*template.Template
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
	tmpl, ok := ctrl.templateCache["index.gohtml"]
	if !ok {
		var err error
		tmpl, err = template.ParseFS(ctrl.templateFS, "index.gohtml")
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot parse template '%s'", "index.gohtml")
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot parse template '%s': %v", "index.gohtml", err))
			return
		}
		if !ctrl.templateDebug {
			ctrl.templateCache["index.gohtml"] = tmpl
		}
	}
	if err := tmpl.Execute(c.Writer, nil); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", "index.gohtml")
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", "index.gohtml", err))
		return
	}
}
