package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"image"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/je4/basel-collections/v2/directus"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/utils/v2/pkg/openai"
	"github.com/je4/utils/v2/pkg/zLogger"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"golang.org/x/text/language"
)

type baseData struct {
	Name       string
	Lang       string
	RootPath   string
	Exhibition bool
	KI         bool
	Params     template.URL
	//Search       template.URL
	Cursor     string
	SearchAddr string
	DetailAddr string
	Page       string
	LoginURL   string
	Self       string
	User       *User
	Mode       string
	Back       string
}

type CollFacetType struct {
	Id int64 `toml:"id" json:"id"`
	//Name       string `toml:"name" json:"name"`
	Count      int    `toml:"count" json:"count"`
	Title      string `toml:"title" json:"title"`
	Url        string `toml:"url" json:"url"`
	Identifier string `toml:"identifier" json:"identifier"`
	Image      string `toml:"image" json:"image"`
	Contact    string `toml:"contact" json:"contact"`
}

// NewController creates a new Controller instance.
func NewController(name, localAddr, externalAddr, searchAddr, detailAddr string, protoHTTP bool, auth map[string]string, cert *tls.Certificate, templateFS, staticFS, dataFS, pagesFS fs.FS, client client.RevCatGraphQLClient, zoomPos map[string][]image.Rectangle, mediaserverBase, mediaserverKey string, mediaserverTokenExp time.Duration, bundle *i18n.Bundle, collections, catalogs, medias []*CollFacetType, fieldMapping map[string]string, embeddings *openai.ClientV2, templateDebug, zoomOnly bool, loginURL, loginIssuer, loginJWTKey string, loginJWTAlgs []string, locations map[string][]net.IPNet, facetInclude, facetExclude []string, baseFilter []*client.InFilter, mode string, logger zLogger.ZLogger) (*Controller, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
			html.WithXHTML(),
		),
	)
	ctrl := &Controller{
		name:                name,
		localAddr:           localAddr,
		externalAddr:        externalAddr,
		searchAddr:          searchAddr,
		detailAddr:          detailAddr,
		protoHTTP:           protoHTTP,
		md:                  md,
		auth:                auth,
		srv:                 nil,
		cert:                cert,
		templateFS:          templateFS,
		staticFS:            staticFS,
		dataFS:              dataFS,
		pagesFS:             pagesFS,
		zoomPos:             zoomPos,
		templateDebug:       templateDebug,
		templateCache:       make(map[string]any),
		logger:              logger,
		client:              client,
		fieldMapping:        fieldMapping,
		mediaserverBase:     mediaserverBase,
		mediaserverKey:      mediaserverKey,
		mediaserverTokenExp: mediaserverTokenExp,
		bundle:              bundle,
		embeddings:          embeddings,
		zoomOnly:            zoomOnly,
		languageMatcher:     language.NewMatcher(bundle.LanguageTags()),
		collections:         collections,
		catalogs:            catalogs,
		medias:              medias,
		loginURL:            loginURL,
		loginIssuer:         loginIssuer,
		loginJWTKey:         loginJWTKey,
		loginJWTAlgs:        loginJWTAlgs,
		locations:           locations,
		facetInclude:        facetInclude,
		facetExclude:        facetExclude,
		baseFilter:          baseFilter,
		mode:                mode,
	}
	ctrl.logger.Info().Msgf("Zoom only: %v", ctrl.zoomOnly)
	if err := ctrl.init(); err != nil {
		return nil, errors.Wrap(err, "cannot initialize controller")
	}
	return ctrl, nil
}

// init initializes the controller, sets up routes and middleware.
func (ctrl *Controller) init() error {
	// create list with type/id and path for markdowns
	ctrl.markdowns = map[string]map[string]string{}
	if ctrl.pagesFS != nil {
		if err := fs.WalkDir(ctrl.pagesFS, ".", func(pathName string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.ToLower(path.Ext(pathName)) != ".md" {
				return nil
			}
			mdData, err := fs.ReadFile(ctrl.pagesFS, pathName)
			if err != nil {
				return errors.Wrapf(err, "cannot read file %s", pathName)
			}
			meta, markdown := ctrl.parseMarkdown(mdData, pathName)
			if meta["type"] == "" || meta["collectiontitle"] == "" || len(markdown) == 0 {
				return nil
			}
			meta["path"] = pathName
			name := strings.ToLower(fmt.Sprintf("%s.%s", meta["type"], meta["collectiontitle"]))
			ctrl.logger.Info().Msgf("adding markdown [%s] --> %s", name, pathName)
			ctrl.markdowns[name] = meta
			return nil
		}); err != nil {
			return errors.Wrap(err, "cannot walk pagesFS")
		}
	}

	// refresh template files to ensure they are up-to-date
	if err := ctrl.refreshTemplateFiles(); err != nil {
		return errors.Wrapf(err, "cannot refresh template files")
	}
	// set up gin router with default configuration and CORS/Auth middleware
	router := gin.Default()
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	router.Use(cors.New(corsConfig), ctrl.AuthHandler)
	if len(ctrl.auth) > 0 {
		router.Use(gin.BasicAuth(ctrl.auth))
	}
	// serve static and data files
	router.StaticFS("/static", NewDefaultIndexFS(http.FS(ctrl.staticFS), "index.html"))
	router.StaticFS("/data", NewDefaultIndexFS(http.FS(ctrl.dataFS), "index.html"))

	if ctrl.pagesFS != nil {
		router.GET("/pages/*any", ctrl.pagePage)
	}

	// version endpoint
	router.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"version": Version,
		})
	})
	// root redirect based on language detection
	router.GET("/", func(c *gin.Context) {
		cookieLang, _ := c.Request.Cookie("lang")
		accept := c.Request.Header.Get("Accept-Language")
		langTag, _ := language.MatchStrings(ctrl.languageMatcher, cookieLang.String(), accept)
		langBase, _ := langTag.Base()
		lang := langBase.String()
		if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
			lang = "en"
		}
		target, err := url.JoinPath(ctrl.externalAddr, "/", lang)
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot join path '%s' and '%s'", ctrl.externalAddr, lang)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot join path '%s' and '%s': %v", ctrl.externalAddr, lang, err))
			return
		}
		c.Redirect(http.StatusTemporaryRedirect, target)
	})

	// language-specific home page or redirect to zoom if configured
	router.GET("/:lang", func(c *gin.Context) {
		lang := c.Param("lang")
		if ctrl.zoomOnly {
			target, err := url.JoinPath(ctrl.externalAddr, "/zoom", lang)
			if err != nil {
				ctrl.logger.Error().Err(err).Msgf("cannot join path '%s' and '%s'", ctrl.externalAddr, lang)
				c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot join path '%s' and '%s': %v", ctrl.externalAddr, lang, err))
				return
			}
			c.Redirect(http.StatusTemporaryRedirect, target)
			return
		}
		ctrl.indexPage(c)
	})

	// imprint page redirects and handlers
	router.GET("/impressum", func(c *gin.Context) {
		cookieLang, _ := c.Request.Cookie("lang")
		accept := c.Request.Header.Get("Accept-Language")
		langTag, _ := language.MatchStrings(ctrl.languageMatcher, cookieLang.String(), accept)
		langBase, _ := langTag.Base()
		lang := langBase.String()
		if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
			lang = "en"
		}
		target, err := url.JoinPath(ctrl.externalAddr, "/impressum", lang)
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot join path '%s' and '%s'", ctrl.externalAddr, lang)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot join path '%s' and '%s': %v", ctrl.externalAddr, lang, err))
			return
		}
		c.Redirect(http.StatusTemporaryRedirect, target)
	})

	router.GET("/impressum/:lang", func(c *gin.Context) {
		lang := c.Param("lang")
		if ctrl.zoomOnly {
			target, err := url.JoinPath(ctrl.externalAddr, "/zoom", lang)
			if err != nil {
				ctrl.logger.Error().Err(err).Msgf("cannot join path '%s' and '%s'", ctrl.externalAddr, lang)
				c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot join path '%s' and '%s': %v", ctrl.externalAddr, lang, err))
				return
			}
			c.Redirect(http.StatusTemporaryRedirect, target)
			return
		}
		ctrl.impressumPage(c)
	})
	// contact page redirects and handlers
	router.GET("/kontakt", func(c *gin.Context) {
		cookieLang, _ := c.Request.Cookie("lang")
		accept := c.Request.Header.Get("Accept-Language")
		langTag, _ := language.MatchStrings(ctrl.languageMatcher, cookieLang.String(), accept)
		langBase, _ := langTag.Base()
		lang := langBase.String()
		if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
			lang = "en"
		}
		target, err := url.JoinPath(ctrl.externalAddr, "/kontakt", lang)
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot join path '%s' and '%s'", ctrl.externalAddr, lang)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot join path '%s' and '%s': %v", ctrl.externalAddr, lang, err))
			return
		}
		c.Redirect(http.StatusTemporaryRedirect, target)
	})

	router.GET("/kontakt/:lang", func(c *gin.Context) {
		lang := c.Param("lang")
		if ctrl.zoomOnly {
			target, err := url.JoinPath(ctrl.externalAddr, "/zoom", lang)
			if err != nil {
				ctrl.logger.Error().Err(err).Msgf("cannot join path '%s' and '%s'", ctrl.externalAddr, lang)
				c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot join path '%s' and '%s': %v", ctrl.externalAddr, lang, err))
				return
			}
			c.Redirect(http.StatusTemporaryRedirect, target)
			return
		}
		ctrl.kontaktPage(c)
	})

	// zoom-related endpoints
	router.GET("/zoom/signature/:PosX/:PosY", ctrl.zoomSignature)
	router.GET("/zoom/:lang", ctrl.zoomPage)
	router.GET("/zoom", func(c *gin.Context) {
		cookieLang, _ := c.Request.Cookie("lang")
		accept := c.Request.Header.Get("Accept-Language")
		langTag, _ := language.MatchStrings(ctrl.languageMatcher, cookieLang.String(), accept)
		langBase, _ := langTag.Base()
		lang := langBase.String()
		if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
			lang = "en"
		}
		newURL, err := url.JoinPath(ctrl.externalAddr, "/", lang)
		if err != nil {
			ctrl.logger.Error().Err(err).Msgf("cannot join path '%s' and '%s'", ctrl.externalAddr, lang)
			c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot join path '%s' and '%s': %v", ctrl.externalAddr, lang, err))
			return
		}
		if c.Request.URL.RawQuery != "" {
			newURL += "?" + c.Request.URL.RawQuery
		}
		c.Redirect(http.StatusTemporaryRedirect, newURL)
	})

	// grid, table, and list view endpoints (search)
	router.GET("/grid", func(c *gin.Context) {
		cookieLang, _ := c.Request.Cookie("lang")
		accept := c.Request.Header.Get("Accept-Language")
		langTag, _ := language.MatchStrings(ctrl.languageMatcher, cookieLang.String(), accept)
		langBase, _ := langTag.Base()
		lang := langBase.String()
		if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
			lang = "en"
		}
		newURL := "/grid/" + lang
		if c.Request.URL.RawQuery != "" {
			newURL += "?" + c.Request.URL.RawQuery
		}
		c.Redirect(http.StatusTemporaryRedirect, newURL)
	})
	router.POST("/grid/:lang", func(c *gin.Context) {
		ctrl.searchPage(c, "grid")
	})
	router.GET("/grid/:lang", func(c *gin.Context) {
		ctrl.searchPage(c, "grid")
	})

	router.GET("/table", func(c *gin.Context) {
		cookieLang, _ := c.Request.Cookie("lang")
		accept := c.Request.Header.Get("Accept-Language")
		langTag, _ := language.MatchStrings(ctrl.languageMatcher, cookieLang.String(), accept)
		langBase, _ := langTag.Base()
		lang := langBase.String()
		if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
			lang = "en"
		}
		newURL := "/table/" + lang
		if c.Request.URL.RawQuery != "" {
			newURL += "?" + c.Request.URL.RawQuery
		}
		c.Redirect(http.StatusTemporaryRedirect, newURL)
	})
	router.POST("/table/:lang", func(c *gin.Context) {
		ctrl.searchPage(c, "table")
	})
	router.GET("/table/:lang", func(c *gin.Context) {
		ctrl.searchPage(c, "table")
	})

	router.GET("/list", func(c *gin.Context) {
		cookieLang, _ := c.Request.Cookie("lang")
		accept := c.Request.Header.Get("Accept-Language")
		langTag, _ := language.MatchStrings(ctrl.languageMatcher, cookieLang.String(), accept)
		langBase, _ := langTag.Base()
		lang := langBase.String()
		if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
			lang = "en"
		}
		newURL := "/list/" + lang
		if c.Request.URL.RawQuery != "" {
			newURL += "?" + c.Request.URL.RawQuery
		}
		c.Redirect(http.StatusTemporaryRedirect, newURL)
	})
	router.POST("/list/:lang", func(c *gin.Context) {
		ctrl.searchPage(c, "list")
	})
	router.GET("/list/:lang", func(c *gin.Context) {
		ctrl.searchPage(c, "list")
	})

	// detail view endpoints
	router.GET("/detailtext/:signature/:lang", func(c *gin.Context) {
		ctrl.detailText(c)
	})
	router.GET("/detailjson/:signature/:lang", func(c *gin.Context) {
		ctrl.detailJSON(c)
	})
	router.GET("/detailtextlist/:collection", func(c *gin.Context) {
		ctrl.detailTextList(c)
	})

	router.GET("/detail/:signature/:lang", func(c *gin.Context) {
		ctrl.detail(c)
	})

	router.GET("/detail/:signature", func(c *gin.Context) {
		cookieLang, _ := c.Request.Cookie("lang")
		accept := c.Request.Header.Get("Accept-Language")
		langTag, _ := language.MatchStrings(ctrl.languageMatcher, cookieLang.String(), accept)
		langBase, _ := langTag.Base()
		lang := langBase.String()
		detailAddr := ctrl.detailAddr
		user := GetUser(c)
		if user.IsLoggedIn() {
			detailAddr = ctrl.searchAddr
		}
		if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
			lang = "en"
		}
		newURL := fmt.Sprintf("%s/detail/%s/%s", detailAddr, c.Param("signature"), lang)
		if c.Request.URL.RawQuery != "" {
			newURL += "?" + c.Request.URL.RawQuery
		}
		c.Redirect(http.StatusTemporaryRedirect, newURL)
	})

	// foliate viewer endpoint for EPUBs
	router.GET("/foliateviewer", func(c *gin.Context) {
		ctrl.foliateViewer(c)
	})

	// configure and initialize the HTTP server
	var tlsConfig *tls.Config
	if ctrl.cert != nil && ctrl.protoHTTP == false {
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{*ctrl.cert},
		}
	}
	ctrl.srv = &http.Server{
		Addr:      ctrl.localAddr,
		Handler:   router,
		TLSConfig: tlsConfig,
	}
	return nil
}

// langAvailable checks if a language is available in the i18n bundle.
func (ctrl *Controller) langAvailable(lang string) bool {
	for _, l := range ctrl.bundle.LanguageTags() {
		b, _ := l.Base()
		if b.String() == lang {
			return true
		}
	}
	return false
}

// getLang extracts the language from the request parameters and falls back to "de" if not available.
func (ctrl *Controller) getLang(c *gin.Context) string {
	lang := c.Param("lang")
	if !ctrl.langAvailable(lang) {
		lang = "de"
	}
	return lang
}

// getMediathekEntry retrieves a mediathek entry from the GraphQL client.
func (ctrl *Controller) getMediathekEntry(c *gin.Context, id string) (*client.MediathekEntries_MediathekEntries, error) {
	if id == "" {
		return nil, errors.New("id missing")
	}

	source, err := ctrl.client.MediathekEntries(c, []string{id})
	if err != nil {
		return nil, errors.Wrapf(err, "cannot get source '%s'", id)
	}
	if source == nil || len(source.MediathekEntries) == 0 {
		return nil, errors.Errorf("source '%s' not found", id)
	}
	return source.MediathekEntries[0], nil
}

// getBaseData populates the baseData struct with request-specific information.
func (ctrl *Controller) getBaseData(c *gin.Context, lang string, rootPath string) baseData {
	user := GetUser(c)
	detailAddr := ctrl.detailAddr
	if user.IsLoggedIn() {
		detailAddr = ctrl.searchAddr
	}

	return baseData{
		Name:       ctrl.name,
		Lang:       lang,
		RootPath:   rootPath,
		SearchAddr: ctrl.searchAddr,
		DetailAddr: detailAddr,
		LoginURL:   ctrl.loginURL,
		Self:       fmt.Sprintf("%s%s", ctrl.externalAddr, c.Request.URL.String()),
		User:       user,
		Mode:       ctrl.mode,
	}
}

type Controller struct {
	localAddr           string
	externalAddr        string
	srv                 *http.Server
	cert                *tls.Certificate
	logger              zLogger.ZLogger
	templateFS          fs.FS
	templateFiles       []string // Liste aller .gohtml und .gotmpl Dateien
	staticFS            fs.FS
	dataFS              fs.FS
	pagesFS             fs.FS
	dir                 *directus.Directus
	templateDebug       bool
	templateCache       map[string]any
	templateMutex       sync.Mutex
	client              client.RevCatGraphQLClient
	mediaserverBase     string
	bundle              *i18n.Bundle
	languageMatcher     language.Matcher
	searchAddr          string
	detailAddr          string
	zoomPos             map[string][]image.Rectangle
	embeddings          *openai.ClientV2
	zoomOnly            bool
	protoHTTP           bool
	auth                map[string]string
	collections         []*CollFacetType
	catalogs            []*CollFacetType
	medias              []*CollFacetType
	fieldMapping        map[string]string
	loginURL            string
	loginIssuer         string
	loginJWTKey         string
	loginJWTAlgs        []string
	locations           map[string][]net.IPNet
	mediaserverKey      string
	mediaserverTokenExp time.Duration
	facetInclude        []string
	facetExclude        []string
	mode                string
	baseFilter          []*client.InFilter
	name                string
	md                  goldmark.Markdown
	markdowns           map[string]map[string]string
}

// Start starts the HTTP/HTTPS server.
func (ctrl *Controller) Start() error {
	go func() {
		if ctrl.srv.TLSConfig == nil {
			fmt.Printf("starting server at http://%s\n", ctrl.localAddr)
			if err := ctrl.srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				// unexpected error. port in use?
				ctrl.logger.Err(err).Msgf("server on '%s' ended", ctrl.localAddr)
			}
		} else {
			fmt.Printf("starting server at https://%s\n", ctrl.localAddr)
			if err := ctrl.srv.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
				// unexpected error. port in use?
				ctrl.logger.Err(err).Msgf("server on '%s' ended", ctrl.localAddr)
			}
		}
		// always returns error. ErrServerClosed on graceful close
	}()

	return nil
}

// Stop stops the HTTP/HTTPS server.
func (ctrl *Controller) Stop() error {
	return ctrl.srv.Shutdown(context.Background())
}
