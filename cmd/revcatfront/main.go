package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/Yamashou/gqlgenc/clientv2"
	"github.com/bluele/gcache"
	"github.com/je4/basel-collections/v2/directus"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/revcatfront/v2/config"
	"github.com/je4/revcatfront/v2/data/certs"
	"github.com/je4/revcatfront/v2/data/web/static"
	"github.com/je4/revcatfront/v2/data/web/templates"
	"github.com/je4/revcatfront/v2/pkg/server"
	"github.com/je4/utils/v2/pkg/openai"
	"github.com/je4/utils/v2/pkg/zLogger"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/rs/zerolog"
	"golang.org/x/text/language"
	"image"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
)

var configfile = flag.String("config", "", "location of toml configuration file")

func auth(apikey string) func(ctx context.Context, req *http.Request, gqlInfo *clientv2.GQLRequestInfo, res interface{}, next clientv2.RequestInterceptorFunc) error {
	return func(ctx context.Context, req *http.Request, gqlInfo *clientv2.GQLRequestInfo, res interface{}, next clientv2.RequestInterceptorFunc) error {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apikey))
		return next(ctx, req, gqlInfo, res)
	}
}

func main() {

	flag.Parse()

	var cfgFS fs.FS
	var cfgFile string
	if *configfile != "" {
		cfgFS = os.DirFS(filepath.Dir(*configfile))
		cfgFile = filepath.Base(*configfile)
	} else {
		cfgFS = config.ConfigFS
		cfgFile = "revcatfront.toml"
	}

	conf := &RevCatFrontConfig{
		LogFile:      "",
		LogLevel:     "DEBUG",
		LocalAddr:    "localhost:81",
		ExternalAddr: "http://localhost:81",
	}

	if err := LoadRevCatFrontConfig(cfgFS, cfgFile, conf); err != nil {
		log.Fatalf("cannot load toml from [%v] %s: %v", cfgFS, cfgFile, err)
	}
	// create logger instance
	var out io.Writer = os.Stdout
	if conf.LogFile != "" {
		fp, err := os.OpenFile(conf.LogFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("cannot open logfile %s: %v", conf.LogFile, err)
		}
		defer fp.Close()
		out = fp
	}

	output := zerolog.ConsoleWriter{Out: out, TimeFormat: time.RFC3339}
	_logger := zerolog.New(output).With().Timestamp().Logger()
	_logger.Level(zLogger.LogLevel(conf.LogLevel))
	var logger zLogger.ZLogger = &_logger

	jsonBytes, _ := json.MarshalIndent(conf, "", "  ")
	logger.Debug().Msgf("config: %s", jsonBytes)

	var localeFS fs.FS
	logger.Debug().Msgf("locale folder: '%s'", conf.Locale.Folder)
	if conf.Locale.Folder == "" {
		localeFS = config.ConfigFS
	} else {
		localeFS = os.DirFS(conf.Locale.Folder)
	}

	glang, err := language.Parse(conf.Locale.Default)
	if err != nil {
		logger.Fatal().Msgf("cannot parse language %s: %v", conf.Locale.Default, err)
	}
	bundle := i18n.NewBundle(glang)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	for _, lang := range conf.Locale.Available {
		localeFile := fmt.Sprintf("active.%s.toml", lang)
		if _, err := fs.Stat(localeFS, localeFile); err != nil {
			logger.Fatal().Msgf("cannot find locale file [%v] %s: %v", reflect.TypeOf(localeFS), localeFile, err)

		}

		if _, err := bundle.LoadMessageFileFS(localeFS, localeFile); err != nil {
			logger.Fatal().Msgf("cannot load locale file [%v] %s: %v", localeFS, localeFile, err)
		}

	}

	var cert *tls.Certificate
	if conf.TLSCert != "" {
		c, err := tls.LoadX509KeyPair(conf.TLSCert, conf.TLSKey)
		if err != nil {
			logger.Fatal().Msgf("cannot load tls certificate: %v", err)
		}
		cert = &c
	} else {
		if strings.HasPrefix(strings.ToLower(conf.ExternalAddr), "https://") {
			certBytes, err := fs.ReadFile(certs.CertFS, "localhost.cert.pem")
			if err != nil {
				logger.Fatal().Msgf("cannot read internal cert")
			}
			keyBytes, err := fs.ReadFile(certs.CertFS, "localhost.key.pem")
			if err != nil {
				logger.Fatal().Msgf("cannot read internal key")
			}
			c, err := tls.X509KeyPair(certBytes, keyBytes)
			if err != nil {
				logger.Fatal().Msgf("cannot create internal cert")
			}
			cert = &c
		}
	}

	var templateFS fs.FS = templates.FS
	if conf.Templates != "" {
		templateFS = os.DirFS(conf.Templates)
	}
	var staticFS fs.FS = static.FS
	if conf.StaticFiles != "" {
		staticFS = os.DirFS(conf.StaticFiles)
	}

	var dataFS fs.FS
	if conf.DataDir != "" {
		dataFS = os.DirFS(conf.DataDir)
	}

	var embeddings *openai.ClientV2
	if string(conf.OpenAIApiKey) != "" {
		kv := openai.NewKVGCache(gcache.New(256).LRU().Build())
		embeddings = openai.NewClientV2(string(conf.OpenAIApiKey), kv, logger)
	}

	dir := directus.NewDirectus(conf.Directus.BaseUrl, string(conf.Directus.Token), time.Duration(conf.Directus.CacheTime))

	if conf.Revcat.Insecure {
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	httpClient := &http.Client{}
	revcatClient := client.NewClient(httpClient, conf.Revcat.Endpoint, nil, func(ctx context.Context, req *http.Request, gqlInfo *clientv2.GQLRequestInfo, res interface{}, next clientv2.RequestInterceptorFunc) error {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", conf.Revcat.Apikey))
		return next(ctx, req, gqlInfo, res)
	})

	var collagePos = map[string][]image.Rectangle{}
	collageFilename := filepath.Join(conf.DataDir, "collage/collage.json")
	fp, err := os.Open(collageFilename)
	if err != nil {
		logger.Panic().Msgf("cannot open %s: %v", collageFilename, err)
	}
	jsonDec := json.NewDecoder(fp)
	if err := jsonDec.Decode(&collagePos); err != nil {
		fp.Close()
		logger.Panic().Msgf("cannot decode %s: %v", collageFilename, err)
	}
	fp.Close()

	var authConfig map[string]string
	if len(conf.Auth) > 0 {
		authConfig = map[string]string{}
		for _, a := range conf.Auth {
			authConfig[a.User] = a.Password
		}
	}

	ctrl, err := server.NewController(
		conf.LocalAddr,
		conf.ExternalAddr,
		conf.SearchAddr,
		conf.DetailAddr,
		conf.ProtoHTTP,
		authConfig,
		cert,
		templateFS,
		staticFS,
		dataFS,
		dir,
		revcatClient,
		collagePos,
		conf.Directus.CatalogID,
		conf.MediaserverBase,
		bundle,
		embeddings,
		conf.ZoomOnly,
		conf.Templates != "",
		logger)
	if err != nil {
		logger.Fatal().Msgf("cannot create controller: %v", err)
	}
	ctrl.Start()

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	fmt.Println("press ctrl+c to stop server")
	s := <-done
	fmt.Println("got signal:", s)

	if err := ctrl.Stop(); err != nil {
		logger.Fatal().Msgf("cannot stop server: %v", err)
	}
	/*
		if conf.Revcat.Insecure {
			http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		httpClient := &http.Client{}
		c := client.NewClient(httpClient, conf.Revcat.Endpoint, nil)
		entries, err := c.MediathekEntries(
			context.Background(),
			[]string{"zotero2-2486551.TJDM3289"},
			auth(string(conf.Revcat.Apikey)),
		)
		if err != nil {
			panic(err)
		}
		for _, entry := range entries.GetMediathekEntries() {
			logger.Info().Msgf("%+v\n", entry)
		}

	*/
}
