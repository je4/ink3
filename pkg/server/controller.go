package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"html/template"
	"image"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	tmpl "text/template"
	"time"

	"emperror.dev/errors"
	"github.com/Masterminds/sprig/v3"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/go-git/go-git/v5/utils/ioutil"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gosimple/slug"
	"github.com/je4/basel-collections/v2/directus"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/utils/v2/pkg/openai"
	"github.com/je4/utils/v2/pkg/zLogger"
	"github.com/je4/zsearch/v2/pkg/translate"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	oai "github.com/sashabaranov/go-openai"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
	"golang.org/x/net/html"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

var languageNamer = map[string]display.Namer{
	"de": display.German.Tags(),
	"en": display.English.Tags(),
	"fr": display.French.Tags(),
	"it": display.Italian.Tags(),
}

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

func NewJWT(secret string, subject string, alg string, valid int64, domain string, issuer string, userId string) (tokenString string, err error) {

	var signingMethod jwt.SigningMethod
	switch strings.ToLower(alg) {
	case "hs256":
		signingMethod = jwt.SigningMethodHS256
	case "hs384":
		signingMethod = jwt.SigningMethodHS384
	case "hs512":
		signingMethod = jwt.SigningMethodHS512
	case "es256":
		signingMethod = jwt.SigningMethodES256
	case "es384":
		signingMethod = jwt.SigningMethodES384
	case "es512":
		signingMethod = jwt.SigningMethodES512
	case "ps256":
		signingMethod = jwt.SigningMethodPS256
	case "ps384":
		signingMethod = jwt.SigningMethodPS384
	case "ps512":
		signingMethod = jwt.SigningMethodPS512
	default:
		return "", errors.Wrapf(err, "invalid signing method %s", alg)
	}
	exp := time.Now().Unix() + valid
	claims := jwt.MapClaims{
		"sub": strings.ToLower(subject),
		"exp": exp,
	}
	// keep jwt short, no empty Fields
	if domain != "" {
		claims["aud"] = domain
	}
	if issuer != "" {
		claims["iss"] = issuer
	}
	if userId != "" {
		claims["user"] = userId
	}

	token := jwt.NewWithClaims(signingMethod, claims)
	//	log.Println("NewJWT( ", secret, ", ", subject, ", ", exp)
	tokenString, err = token.SignedString([]byte(secret))
	return tokenString, err
}

func (ctrl *Controller) funcMap(name string) template.FuncMap {
	fm := sprig.FuncMap()

	fm["vocTag"] = func(tag string) []string {
		if strings.HasPrefix(tag, "voc:") {
			parts := strings.Split(tag[4:], ":")
			if len(parts) == 2 && strings.HasPrefix(parts[0], "voc_") && strings.HasPrefix(parts[1], "voc_") {
				return parts
			}
		}
		return []string{}
	}

	fm["qrCode"] = func(s string) template.URL {
		qrc, err := qrcode.NewWith(s,
			qrcode.WithEncodingMode(qrcode.EncModeByte),
			qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionQuart),
		)
		if err != nil {
			return template.URL(fmt.Sprintf("cannot create qr code for %s: %v", s, err))
		}
		buf := bytes.NewBuffer(nil)
		wr := ioutil.WriteNopCloser(buf)
		w2 := standard.NewWithWriter(wr, standard.WithQRWidth(40), standard.WithBgTransparent(), standard.WithBuiltinImageEncoder(standard.PNG_FORMAT))
		if err = qrc.Save(w2); err != nil {
			fmt.Printf("cannot save qr code for %s: %v", s, err)
		}
		w2.Close()
		wr.Close()
		return template.URL(fmt.Sprintf("data:image/png;base64,%s", base64.StdEncoding.EncodeToString(buf.Bytes())))
	}

	fm["langName"] = func(langSrc, langTarget string) string {
		if namer, ok := languageNamer[langTarget]; ok {
			return namer.Name(language.MustParse(langSrc))
		}
		return langSrc
	}

	fm["runeString"] = func(r rune) string {
		return string(r)
	}

	fm["digits"] = func(num int) []rune {
		return []rune(strconv.Itoa(num))
	}

	fm["ptrString"] = func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}

	fm["toHTMLif"] = func(s string) any {
		tokens, err := html.ParseFragment(bytes.NewBuffer([]byte(s)), nil)
		if err != nil {
			return s
		}
		if len(tokens) == 0 {
			return s
		}
		token := tokens[0]
		var crawler func(node *html.Node) int64
		crawler = func(node *html.Node) int64 {
			var num int64
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				num += crawler(child)
			}
			if len(node.Data) > 0 {
				if node.Type == html.ElementNode &&
					!slices.Contains([]string{"html", "head", "body"}, node.Data) {
					num++
				}
			}
			return num
		}
		numToken := crawler(token)
		if numToken > 0 {
			return template.HTML(s)
		}
		return s
	}
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
			return key
			// return fmt.Sprintf("cannot localize '%s': %v", key, err)
		}
		return result // fmt.Sprintf("%s (%s)", result, lang)
	}
	fm["slug"] = func(s string, lang string) string {
		return strings.Replace(slug.MakeLang(s, lang), "-", "_", -1)
	}
	fm["map"] = func(kvList []*client.KeyValueFragment, key string) string {
		for _, kv := range kvList {
			if kv.Key == key {
				return kv.Value
			}
		}
		return ""
	}

	fm["calcAspectSize"] = CalcAspectSize

	fm["multiLang"] = func(mf []*client.MultiLangFragment) *translate.MultiLangString {
		m := &translate.MultiLangString{}
		for _, f := range mf {
			lang, _ := language.Parse(f.Lang)
			m.Set(f.Value, lang, f.Translated)
		}
		return m
	}
	fm["name"] = func() string { return name }
	var checkHTMLRegexp = regexp.MustCompile(`<\/?[a-zA-Z][\s\S]*>`)
	fm["nl2br"] = func(s string) string {
		if checkHTMLRegexp.MatchString(s) {
			return s
		}
		return strings.Replace(s, "\n", "<br>\n", -1)
	}
	mediaMatch := regexp.MustCompile(`^mediaserver:([^/]+)/([^/]+)$`)
	fm["medialink"] = func(uri, action, param string, token bool) string {
		matches := mediaMatch.FindStringSubmatch(uri)
		params := strings.Split(param, "/")
		sort.Strings(params)
		// if not matching, just return the uri
		if matches == nil {
			return uri
		}
		collection := matches[1]
		signature := matches[2]
		urlstr := fmt.Sprintf("%s/%s/%s/%s/%s", ctrl.mediaserverBase, collection, signature, action, param)
		if token {
			jwt, err := NewJWT(
				ctrl.mediaserverKey,
				strings.TrimRight(fmt.Sprintf("mediaserver:%s/%s/%s/%s", collection, signature, action, strings.Join(params, "/")), "/"),
				"HS256",
				int64(ctrl.mediaserverTokenExp.Seconds()),
				"mediaserver",
				"mediathek",
				"")
			if err != nil {
				return fmt.Sprintf("ERROR: %v", err)
			}
			urlstr = fmt.Sprintf("%s?token=%s", urlstr, jwt)
		}
		return urlstr
	}

	return fm
}

func NewController(localAddr, externalAddr, searchAddr, detailAddr string, protoHTTP bool, auth map[string]string, cert *tls.Certificate, templateFS, staticFS, dataFS fs.FS, client client.RevCatGraphQLClient, zoomPos map[string][]image.Rectangle, mediaserverBase, mediaserverKey string, mediaserverTokenExp time.Duration, bundle *i18n.Bundle, collections []*CollFacetType, fieldMapping map[string]string, embeddings *openai.ClientV2, templateDebug, zoomOnly bool, loginURL, loginIssuer, loginJWTKey string, loginJWTAlgs []string, locations map[string][]net.IPNet, facetInclude, facetExclude []string, mode string, logger zLogger.ZLogger) (*Controller, error) {

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
		loginURL:            loginURL,
		loginIssuer:         loginIssuer,
		loginJWTKey:         loginJWTKey,
		loginJWTAlgs:        loginJWTAlgs,
		locations:           locations,
		facetInclude:        facetInclude,
		facetExclude:        facetExclude,
		mode:                mode,
	}
	ctrl.logger.Info().Msgf("Zoom only: %v", ctrl.zoomOnly)
	if err := ctrl.init(); err != nil {
		return nil, errors.Wrap(err, "cannot initialize controller")
	}
	return ctrl, nil
}

type loginClaim struct {
	jwt.RegisteredClaims
	UserID    any    `json:"userId"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	HomeOrg   string `json:"homeOrg"`
	Groups    string `json:"groups"`
}

type User struct {
	UserID    string   `json:"userId"`
	Email     string   `json:"email"`
	FirstName string   `json:"firstName"`
	LastName  string   `json:"lastName"`
	HomeOrg   string   `json:"homeOrg"`
	Groups    []string `json:"groups"`
}

func (user *User) IsLoggedIn() bool {
	return !(len(user.Groups) == 0 || (len(user.Groups) == 1 && user.Groups[0] == "global/guest"))
}

func (ctrl *Controller) AuthHandler(ctx *gin.Context) {
	hasCookie := false
	bearerToken := ctx.Request.Header.Get("Authorization")
	if bearerToken == "" {
		bearerToken = ctx.Request.URL.Query().Get("token")
	} else {
		if bearerToken[:7] != "Bearer " {
			ctx.Next()
			return
		}
		bearerToken = bearerToken[7:]
	}
	if bearerToken == "" {
		if cookie, err := ctx.Cookie("token"); err == nil {
			bearerToken = cookie
			hasCookie = true
		}
	}
	if bearerToken == "" {
		ctx.Next()
		return
	}

	claim := &loginClaim{}
	token, err := jwt.ParseWithClaims(bearerToken, claim, func(token *jwt.Token) (interface{}, error) {
		talg := token.Method.Alg()
		algOK := false
		for _, a := range ctrl.loginJWTAlgs {
			if talg == a {
				algOK = true
				break
			}
		}
		if !algOK {
			ctx.SetCookie("token", "", -1, "/", "", false, false)
			return false, fmt.Errorf("unexpected signing method (allowed are %v): %v", ctrl.loginJWTAlgs, token.Header["alg"])
		}
		return []byte(ctrl.loginJWTKey), nil
	})
	if err != nil {
		ctx.SetCookie("token", "", -1, "/", "", false, false)
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, fmt.Sprintf("cannot parse token: %v", err))
		return
	}
	if !token.Valid {
		// remove cookie
		ctx.SetCookie("token", "", -1, "/", "", false, false)
		//		ctx.AbortWithStatusJSON(http.StatusUnauthorized, "invalid token")
		ctx.Next()
		return
	}
	if !slices.Contains([]string{ctrl.loginIssuer, "revcatfront"}, claim.Issuer) {
		ctx.SetCookie("token", "", -1, "/", "", false, false)
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, fmt.Sprintf("invalid issuer: %s", claim.Issuer))
		return
	}
	user := &User{
		UserID:    fmt.Sprintf("%v", claim.UserID),
		Email:     claim.Email,
		FirstName: claim.FirstName,
		LastName:  claim.LastName,
		HomeOrg:   claim.HomeOrg,
		Groups:    ctrl.locationGroups(ctx),
	}
	if claim.Groups != "" {
		user.Groups = append(user.Groups, strings.Split(claim.Groups, ";")...)
	}
	ctx.Set("user", user)
	if !hasCookie {
		claim.Issuer = "revcatfront"
		claim.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Hour * 8))
		claim.IssuedAt = jwt.NewNumericDate(time.Now())
		if newTokenString, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claim).SignedString([]byte(ctrl.loginJWTKey)); err == nil {
			ctx.SetCookie("token", newTokenString, 60*23, "/", "", false, false)
		}
	}
	ctx.Next()
}

func GetUser(ctx *gin.Context) *User {
	userAny, ok := ctx.Get("user")
	if !ok {
		return &User{
			Groups: []string{"global/guest"},
		}
	}
	user, ok := userAny.(*User)
	if !ok {
		return &User{
			Groups: []string{"global/guest"},
		}
	}
	return user
}

func (ctrl *Controller) init() error {
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
}

func (ctrl *Controller) locationGroups(ctx *gin.Context) []string {
	ip := net.ParseIP(ctx.ClientIP())
	if ip == nil {
		return []string{}
	}
	groups := []string{}
	for location, nets := range ctrl.locations {
		for _, n := range nets {
			if n.Contains(ip) {
				groups = append(groups, location)
				break
			}
		}
	}
	return groups
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
	impressumTemplate, err := ctrl.loadHTMLTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}

	type tplData struct {
		baseData
		Collections map[int64]*CollFacetType `json:"collections"`
	}
	var data = &tplData{
		Collections: map[int64]*CollFacetType{},
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
	result, err := ctrl.client.Search(c, "", []*client.InFacet{collFacet}, nil, nil, nil, &size, nil, sort)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", "")
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", "", err))
		return
	}

	for _, coll := range ctrl.collections {
		data.Collections[coll.Id] = coll
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
	impressumTemplate, err := ctrl.loadHTMLTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}

	type tplData struct {
		baseData
		Collections map[int64]*CollFacetType `json:"collections"`
	}
	var data = &tplData{
		Collections: map[int64]*CollFacetType{},
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
	result, err := ctrl.client.Search(c, "", []*client.InFacet{collFacet}, nil, nil, nil, &size, nil, sort)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", "")
		c.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", "", err))
		return
	}

	for _, coll := range ctrl.collections {
		data.Collections[coll.Id] = coll
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
	indexTemplate, err := ctrl.loadHTMLTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot load template '%s'", templateName)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot load template '%s': %v", templateName, err))
		return
	}

	type tplData struct {
		baseData
		Collections map[int64]*CollFacetType `json:"collections"`
	}
	var data = &tplData{
		Collections: map[int64]*CollFacetType{},
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
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("unknown collection identifier '%s'", coll.Identifier))
			return
		}
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
	facets := []*client.InFacet{}
	if len(collFacet.Query.BoolTerm.Values) > 0 {
		facets = append(facets, collFacet)
	}
	result, err := ctrl.client.Search(ctx, "", facets, filter, nil, nil, &size, nil, sort)
	if err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot search for '%s'", "")
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot search for '%s': %v", "", err))
		return
	}

	//var str string
	for _, coll := range ctrl.collections {
		data.Collections[coll.Id] = coll
		/*
			str += fmt.Sprintf("[[collection]]\n")
			str += fmt.Sprintf("id = %d\n", coll.Id)
			str += fmt.Sprintf("identifier = \"%s\"\n", strings.Replace(coll.Identifier, "\"", "\\\"", -1))
			str += fmt.Sprintf("title = \"%s\"\n", strings.Replace(coll.GetTitle(), "\"", "\\\"", -1))
			str += fmt.Sprintf("url = \"%s\"\n", coll.GetUrl())
			str += fmt.Sprintf("image = \"%s\"\n\n", coll.Image)
		*/
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
		}
	}

	if err := indexTemplate.Execute(ctx.Writer, data); err != nil {
		ctrl.logger.Error().Err(err).Msgf("cannot execute template '%s'", templateName)
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, fmt.Sprintf("cannot execute template '%s': %v", templateName, err))
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
		tpl, err = template.New(name).Funcs(ctrl.funcMap(name)).ParseFS(ctrl.templateFS, files...)
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
		tpl, err = tmpl.New(name).Funcs(ctrl.funcMap(name)).ParseFS(ctrl.templateFS, files...)
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
	gridTemplate, err := ctrl.loadHTMLTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
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
	result, err = ctrl.client.Search(c, queryString, []*client.InFacet{collFacet, vocFacet}, filter, embedding64, nil, nil, &cursorString, sort)
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

	tpl, err := ctrl.loadTextTemplate(templateName, []string{templateName})
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
	tpl, err := ctrl.loadHTMLTemplate(templateName, []string{"foliatejsviewer.gohtml"})
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
	vocabularyString := c.Query("vocabulary")
	ki := c.Request.URL.Query().Has("ki")
	query := url.Values{}
	if searchString != "" {
		query.Set("search", searchString)
	}
	if collectionsString != "" {
		query.Set("collections", collectionsString)
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
	textTemplate, err := ctrl.loadHTMLTemplate(templateName, []string{
		"head.gohtml",
		"footer.gohtml",
		"nav.gohtml",
		"detail_image.gohtml",
		"detail_video.gohtml",
		"detail_audio.gohtml",
		"detail_pdf_dflip.gohtml",
		"detail_verovio.gohtml",
		"detail_webrecorder.gohtml",
		"detail_epub_foliate.gohtml",
		//"detail_pdf_pdfjs.gohtml",
		//"detail_pdf_3dflipbook.gohtml",
		templateName})
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
	zoomTemplate, err := ctrl.loadHTMLTemplate(templateName, []string{"head.gohtml", "footer.gohtml", "nav.gohtml", templateName})
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
