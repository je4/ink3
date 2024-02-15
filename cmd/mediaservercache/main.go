package main

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"emperror.dev/errors"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Yamashou/gqlgenc/clientv2"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/revcatfront/v2/config"
	"github.com/je4/revcatfront/v2/pkg/server"
	"github.com/je4/utils/v2/pkg/zLogger"
	"github.com/rs/zerolog"
	"image"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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
	switch strings.ToUpper(conf.LogLevel) {
	case "DEBUG":
		_logger = _logger.Level(zerolog.DebugLevel)
	case "INFO":
		_logger = _logger.Level(zerolog.InfoLevel)
	case "WARN":
		_logger = _logger.Level(zerolog.WarnLevel)
	case "ERROR":
		_logger = _logger.Level(zerolog.ErrorLevel)
	case "FATAL":
		_logger = _logger.Level(zerolog.FatalLevel)
	case "PANIC":
		_logger = _logger.Level(zerolog.PanicLevel)
	default:
		_logger = _logger.Level(zerolog.DebugLevel)
	}
	var logger zLogger.ZLogger = &_logger

	var collagePos = map[string][]image.Rectangle{}
	collageFilename := filepath.Join(conf.DataDir, "collage.json")
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

	if conf.Revcat.Insecure {
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	httpClient := &http.Client{}
	revcatClient := client.NewClient(httpClient, conf.Revcat.Endpoint, nil, func(ctx context.Context, req *http.Request, gqlInfo *clientv2.GQLRequestInfo, res interface{}, next clientv2.RequestInterceptorFunc) error {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", conf.Revcat.Apikey))
		return next(ctx, req, gqlInfo, res)
	})

	for signature, _ := range collagePos {
		entries, err := revcatClient.MediathekEntries(context.Background(), []string{signature})
		if err != nil {
			logger.Error().Err(err).Msgf("cannot get mediathek entry for %s", signature)
			break
		}
		for _, entry := range entries.GetMediathekEntries() {
			logger.Info().Msgf("entry: %v", entry)
			item := entry.Base.Poster
			if !strings.HasPrefix(item.URI, "mediaserver:") {
				logger.Warn().Msgf("item %s is not a mediaserver item", item.URI)
				continue
			}
			item.URI = strings.ToLower(item.URI)
			logger.Info().Msgf("item: %s", item.URI)

			switch item.GetName() {
			case "image":
				path := fmt.Sprintf("%s/%s%s", conf.MediaserverBase, strings.TrimPrefix(item.URI, "mediaserver:"), "/resize/size200x200/formatPNG/autorotate")
				if err := cacheItem(path, filepath.Join(conf.DataDir, "mediaserver"), logger); err != nil {
					logger.Panic().Err(err).Msgf("cannot cache item %s", path)
				}
			}

			for _, media := range entry.Media {
				for _, item := range media.GetItems() {
					if !strings.HasPrefix(item.URI, "mediaserver:") {
						logger.Warn().Msgf("item %s is not a mediaserver item", item.URI)
						continue
					}
					logger.Info().Msgf("item: %s", item.URI)
					var actions []string

					switch media.GetName() {
					case "image":
						actions = []string{
							"/resize/size800x400/formatJPEG/autorotate",
							"/resize/size100x1000/formatPNG/autorotate",
						}
					case "audio":
						actions = []string{
							"/resize/size640x480/formatPNG/autorotate",
							"$$web$$1/master",
						}
					case "video":
						size := server.CalcAspectSize(item.Width, item.Height, 600, 480)
						actions = []string{
							fmt.Sprintf("$$timeshot$$3/resize/size%sx%s/formatPNG/autorotate", size.Width, size.Height),
							"$$web/master",
						}
					case "pdf":
						actions = []string{
							"/master",
						}
					}
					for _, action := range actions {
						path := fmt.Sprintf("%s/%s%s", conf.MediaserverBase, strings.TrimPrefix(item.URI, "mediaserver:"), action)
						if err := cacheItem(path, filepath.Join(conf.DataDir, "mediaserver"), logger); err != nil {
							logger.Panic().Err(err).Msgf("cannot cache item %s", path)
						}
					}
				}
			}
		}
	}
}

var mediaserverRegexp = regexp.MustCompile(`^/([^/]+)/([^/]+)/([^/]+)(/.+)?$`)

func cacheItem(uStr string, dir string, logger zLogger.ZLogger) error {
	logger.Info().Msgf("caching %s", uStr)
	u, err := url.Parse(uStr)
	if err != nil {
		return errors.Wrapf(err, "cannot parse url %s", uStr)
	}
	parts := mediaserverRegexp.FindStringSubmatch(strings.ToLower(u.Path))
	if parts == nil {
		return errors.New(fmt.Sprintf("invalid path: %s", u.Path))
	}
	params := strings.Split(strings.Trim(parts[4], "/"), "/")
	slices.Sort(params)
	u.Path = strings.TrimRight(fmt.Sprintf("/%s/%s/%s/%s", parts[1], parts[2], parts[3], strings.Join(params, "/")), "/")

	shaSum := fmt.Sprintf("%x", sha1.Sum([]byte(u.Path)))
	cacheFile := filepath.Join(dir, "cache", string(shaSum[0]), shaSum)
	if _, err := os.Stat(cacheFile); err == nil {
		logger.Info().Msgf("cache hit: %s", uStr)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0755); err != nil && !errors.Is(err, fs.ErrExist) {
		return errors.Wrapf(err, "cannot create cache folder %s", filepath.Dir(cacheFile))
	}
	fp, err := os.Create(cacheFile)
	if err != nil {
		return errors.Wrapf(err, "cannot create cache file %s", cacheFile)
	}
	client := http.Client{
		Timeout: 3600 * time.Second,
	}
	resp, err := client.Get(uStr)
	if err != nil {
		fp.Close()
		os.Remove(cacheFile)
		return errors.Wrapf(err, "cannot load url %s", uStr)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		fp.Close()
		os.Remove(cacheFile)
		return errors.New(fmt.Sprintf("cannot get image %s: %v - %s", uStr, resp.StatusCode, resp.Status))
	}
	if _, err := io.Copy(fp, resp.Body); err != nil {
		fp.Close()
		os.Remove(cacheFile)
		return errors.Wrapf(err, "cannot write cache file %s", cacheFile)
	}
	fp.Close()
	if err := os.WriteFile(cacheFile+".mime", []byte(resp.Header.Get("Content-Type")), 0644); err != nil {
		os.Remove(cacheFile)
		return errors.Wrapf(err, "cannot write mime file %s", cacheFile+".mime")
	}
	return nil
}
