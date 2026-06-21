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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/go-git/go-git/v5/utils/ioutil"
	"github.com/je4/basel-collections/v2/directus"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/utils/v2/pkg/openai"
	"github.com/je4/utils/v2/pkg/zLogger"
	"github.com/je4/zsearch/v2/pkg/translate"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	oai "github.com/sashabaranov/go-openai"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
	"golang.org/x/text/language"
)

type baseData struct {
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

func NewController(localAddr, externalAddr, searchAddr, detailAddr string, protoHTTP bool, auth map[string]string, cert *tls.Certificate, templateFS, staticFS, dataFS fs.FS, client client.RevCatGraphQLClient, zoomPos map[string][]image.Rectangle, mediaserverBase, mediaserverKey string, mediaserverTokenExp time.Duration, bundle *i18n.Bundle, collections, catalogs []*CollFacetType, fieldMapping map[string]string, embeddings *openai.ClientV2, templateDebug, zoomOnly bool, loginURL, loginIssuer, loginJWTKey string, loginJWTAlgs []string, locations map[string][]net.IPNet, facetInclude, facetExclude []string, baseFilter []*client.InFilter, mode string, logger zLogger.ZLogger) (*Controller, error) {

	ctrl := &Controller{
		localAddr:           localAddr,
		externalAddr:        externalAddr,
		searchAddr:          searchAddr,
		detailAddr:          detailAddr,
		protoHTTP:           protoHTTP,
		auth:                auth,
		srv:                 nil,
		cert:                cert,
		templateFS:          templateFS,
		staticFS:            staticFS,
		dataFS:              dataFS,
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

func (ctrl *Controller) init() error {
	if err := ctrl.refreshTemplateFiles(); err != nil {
		return errors.Wrapf(err, "cannot refresh template files")
	}
	router := gin.Default()
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	router.Use(cors.New(corsConfig), ctrl.AuthHandler)
	if len(ctrl.auth) > 0 {
		router.Use(gin.BasicAuth(ctrl.auth))
	}
	router.StaticFS("/static", NewDefaultIndexFS(http.FS(ctrl.staticFS), "index.html"))
	router.StaticFS("/data", NewDefaultIndexFS(http.FS(ctrl.dataFS), "index.html"))

	router.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"version": Version,
		})
	})
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

	router.GET("/foliateviewer", func(c *gin.Context) {
		ctrl.foliateViewer(c)
	})

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

func (ctrl *Controller) langAvailable(lang string) bool {
	for _, l := range ctrl.bundle.LanguageTags() {
		if l.String() == lang {
			return true
		}
	}
	return false
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
	catalogs            []*CollFacetType
}

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

func (ctrl *Controller) Stop() error {
	return ctrl.srv.Shutdown(context.Background())
}

func (ctrl *Controller) impressumPage(c *gin.Context) {
	var lang = c.Param("lang")
	if lang == "" {
		lang = "de"
	}
	if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
		lang = "de"
	}

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
		baseData: baseData{
			Lang:       lang,
			RootPath:   "../../",
			SearchAddr: ctrl.searchAddr,
			LoginURL:   ctrl.loginURL,
			Self:       c.Request.URL.String(),
			User:       GetUser(c),
			Mode:       ctrl.mode,
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
	var lang = c.Param("lang")
	if lang == "" {
		lang = "de"
	}
	if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
		lang = "de"
	}

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
		baseData: baseData{
			Lang:       lang,
			RootPath:   "../../",
			SearchAddr: ctrl.searchAddr,
			LoginURL:   ctrl.loginURL,
			Self:       c.Request.URL.String(),
			User:       GetUser(c),
			Mode:       ctrl.mode,
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
	var lang = ctx.Param("lang")
	if lang == "" {
		lang = "de"
	}
	if !slices.Contains([]string{"de", "en", "fr", "it"}, lang) {
		lang = "de"
	}

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
		baseData: baseData{
			Lang:       lang,
			RootPath:   "",
			SearchAddr: ctrl.searchAddr,
			DetailAddr: ctrl.detailAddr,
			LoginURL:   ctrl.loginURL,
			Self:       fmt.Sprintf("%s%s", ctrl.externalAddr, ctx.Request.URL.Path),
			User:       GetUser(ctx),
			Mode:       ctrl.mode,
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
	result, err = ctrl.client.Search(c, queryString, []*client.InFacet{collFacet, catFacet, vocFacet}, filter, embedding64, nil, nil, &cursorString, sort)
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

func (ctrl *Controller) detailJSON(c *gin.Context) {
	var lang = c.Param("lang")
	if !ctrl.langAvailable(lang) {
		lang = "de"
	}
	id := c.Param("signature")
	if id == "" {
		ctrl.logger.Error().Msgf("id missing")
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("id missing"))
		return
	}

	source, err := ctrl.client.MediathekEntries(c, []string{id})
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
	c.JSON(http.StatusOK, source.MediathekEntries[0])
}

func (ctrl *Controller) detailText(c *gin.Context) {
	var lang = c.Param("lang")
	if !ctrl.langAvailable(lang) {
		lang = "de"
	}
	templateName := "detail_text.gotmpl"
	id := c.Param("signature")
	if id == "" {
		ctrl.logger.Error().Msgf("id missing")
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("id missing"))
		return
	}

	source, err := ctrl.client.MediathekEntries(c, []string{id})
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
			Lang:       lang,
			RootPath:   "../",
			SearchAddr: ctrl.searchAddr,
			LoginURL:   ctrl.loginURL,
			Self:       fmt.Sprintf("%s%s", ctrl.externalAddr, c.Request.URL.Path),
			User:       GetUser(c),
			Mode:       ctrl.mode,
		},
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
	//	c.Set("Content-Type", "text/markdown; charset=utf-8")
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
	var lang = c.Param("lang")
	if !ctrl.langAvailable(lang) {
		lang = "de"
	}
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
	if id == "" {
		ctrl.logger.Error().Err(err).Msgf("signature missing")
		c.AbortWithStatusJSON(http.StatusBadRequest, fmt.Sprintf("signature missing"))
		return
	}

	source, err := ctrl.client.MediathekEntries(c, []string{id})
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
	user := GetUser(c)
	detailAddr := ctrl.detailAddr
	if user.IsLoggedIn() {
		detailAddr = ctrl.searchAddr
	}
	me := source.MediathekEntries[0]
	categories := me.GetBase().GetCategory()
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
	me.Base.Category = newCategories
	var data = &tplData{
		Source:       source.MediathekEntries[0],
		IFrame:       isIFrame,
		SearchSource: sourceString,
		baseData: baseData{
			Lang:       lang,
			RootPath:   "../../",
			Exhibition: isExhibition,
			SearchAddr: ctrl.searchAddr,
			DetailAddr: detailAddr,
			//Search:     template.URL(fmt.Sprintf("%s/search/%s%s", ctrl.searchAddr, lang, searchParams)),
			Params:   template.URL(strings.TrimPrefix(searchParams, "?")),
			LoginURL: ctrl.loginURL,
			Self:     fmt.Sprintf("%s%s", ctrl.externalAddr, c.Request.URL.Path),
			User:     user,
			Mode:     ctrl.mode,
		},
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
