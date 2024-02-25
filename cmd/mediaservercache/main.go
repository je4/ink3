package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/sha512"
	"crypto/tls"
	"emperror.dev/errors"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Yamashou/gqlgenc/clientv2"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/revcatfront/v2/config"
	"github.com/je4/revcatfront/v2/data/certs"
	"github.com/je4/revcatfront/v2/pkg/server"
	"github.com/je4/utils/v2/pkg/zLogger"
	"github.com/rs/cors"
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
var initFlag = flag.Bool("init", false, "initialize mediaserver cache")

func auth(apikey string) func(ctx context.Context, req *http.Request, gqlInfo *clientv2.GQLRequestInfo, res interface{}, next clientv2.RequestInterceptorFunc) error {
	return func(ctx context.Context, req *http.Request, gqlInfo *clientv2.GQLRequestInfo, res interface{}, next clientv2.RequestInterceptorFunc) error {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apikey))
		return next(ctx, req, gqlInfo, res)
	}
}

func main() {

	/*

	 */

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

	if *initFlag {
		initMediaserver(conf, logger)
		return
	}

	var cert *tls.Certificate
	if conf.TLSCert != "" {
		c, err := tls.LoadX509KeyPair(conf.TLSCert, conf.TLSKey)
		if err != nil {
			logger.Fatal().Msgf("cannot load tls certificate: %v", err)
		}
		cert = &c
	} else {
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

	remote, err := url.Parse(conf.MediaServer.BaseDir)
	if err != nil {
		logger.Panic().Err(err)
	}

	handler := func() func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			logger.Info().Msgf("Request: %v", r.URL)
			parts := mediaserverRegexp.FindStringSubmatch(strings.TrimLeft(r.URL.Path, "/"))
			if parts == nil {
				http.Error(w, fmt.Sprintf("invalid path: %s", r.URL.Path), http.StatusBadRequest)
				return
			}
			params := strings.Split(strings.Trim(strings.ToLower(parts[4]), "/"), "/")
			slices.Sort(params)
			r.Host = remote.Host
			r.URL.Path = strings.TrimRight(fmt.Sprintf("%s/%s/%s/%s", parts[1], parts[2], parts[3], strings.Join(params, "/")), "/")

			shaSum := fmt.Sprintf("%x", sha1.Sum([]byte(r.URL.Path)))

			cacheFile := filepath.Join(conf.MediaServer.BaseDir, "cache", string(shaSum[0]), string(shaSum[1]), shaSum)
			fi, err := os.Stat(cacheFile)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					http.Error(w, fmt.Sprintf("cannot stat cache file %s: %v", cacheFile, err), http.StatusInternalServerError)
					return
				} else {
					http.Error(w, fmt.Sprintf("cache miss: %s", r.URL.Path), http.StatusNotFound)
					return
				}
			}
			logger.Info().Msgf("cache hit: %s", r.URL.Path)
			mimeFilename := cacheFile + ".mime"
			mimeStr, err := os.ReadFile(mimeFilename)
			if err != nil {
				http.Error(w, fmt.Sprintf("cannot read header file %s: %v", mimeFilename, err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", string(mimeStr))
			fp, err := os.Open(cacheFile)
			if err != nil {
				http.Error(w, fmt.Sprintf("cannot open cache file %s: %v", cacheFile, err), http.StatusInternalServerError)
				return
			}
			defer fp.Close()
			http.ServeContent(w, r, cacheFile, fi.ModTime(), fp)
			return
		}
	}

	router := http.NewServeMux()
	router.HandleFunc("/", handler())
	cHandler := cors.Default().Handler(router)

	var tlsConfig *tls.Config
	if cert != nil {
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{*cert},
		}
	}
	srv := &http.Server{
		Addr:      conf.MediaServer.LocalAddr,
		Handler:   cHandler,
		TLSConfig: tlsConfig,
	}

	fmt.Printf("starting server at https://%s\n", conf.MediaServer.LocalAddr)
	if err := srv.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
		// unexpected error. port in use?
		fmt.Errorf("server on '%s' ended: %v", conf.LocalAddr, err)
	}

}

func initMediaserver(conf *RevCatFrontConfig, logger zLogger.ZLogger) {
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

	if conf.Revcat.Insecure {
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	httpClient := &http.Client{}
	revcatClient := client.NewClient(httpClient, conf.Revcat.Endpoint, nil, func(ctx context.Context, req *http.Request, gqlInfo *clientv2.GQLRequestInfo, res interface{}, next clientv2.RequestInterceptorFunc) error {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", conf.Revcat.Apikey))
		return next(ctx, req, gqlInfo, res)
	})

	var num int = 0
	for signature, _ := range collagePos {
		entries, err := revcatClient.MediathekEntries(context.Background(), []string{signature})
		if err != nil {
			logger.Error().Err(err).Msgf("cannot get mediathek entry for %s", signature)
			continue
		}
		logger.Info().Msgf("entry [% 4d/%04d]: %s", num, len(collagePos), signature)
		num++
		for _, entry := range entries.GetMediathekEntries() {
			if entry.Base.Poster != nil {
				if !strings.HasPrefix(entry.Base.Poster.URI, "mediaserver:") {
					logger.Warn().Msgf("item %s is not a mediaserver item", entry.Base.Poster.URI)
					continue
				}
				//entry.Base.Poster.URI = strings.ToLower(entry.Base.Poster.URI)
				logger.Info().Msgf("item: %s", entry.Base.Poster.URI)

				switch entry.Base.Poster.GetType() {
				case "image":
					path := fmt.Sprintf("%s%s", strings.TrimPrefix(entry.Base.Poster.URI, "mediaserver:"), "/resize/size200x200/formatPNG/autorotate")
					if err := cacheItem(conf.MediaserverBase, path, filepath.Join(conf.DataDir, "mediaserver"), logger); err != nil {
						logger.Panic().Err(err).Msgf("cannot cache item %s", path)
					}
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

					switch item.GetType() {
					case "image":
						actions = []string{
							"/resize/size800x400/formatJPEG/autorotate",
							"/resize/size100x1000/formatPNG/autorotate",
						}
					case "audio":
						actions = []string{
							"$$poster/resize/size640x480/formatPNG/autorotate",
							"$$web$$1/master",
						}
					case "video":
						size := server.CalcAspectSize(item.Width, item.Height, 600, 480)
						actions = []string{
							fmt.Sprintf("$$timeshot$$3/resize/size%dx%d/formatPNG/autorotate", size.Width, size.Height),
							"$$web/master",
							fmt.Sprintf("$$timeshot$$%d/resize/size%dx%d/formatPNG/autorotate", 3, size.Width/5, size.Height/5),
							fmt.Sprintf("$$timeshot$$%d/resize/size%dx%d/formatPNG/autorotate", 8, size.Width/5, size.Height/5),
							fmt.Sprintf("$$timeshot$$%d/resize/size%dx%d/formatPNG/autorotate", 12, size.Width/5, size.Height/5),
							fmt.Sprintf("$$timeshot$$%d/resize/size%dx%d/formatPNG/autorotate", 17, size.Width/5, size.Height/5),
							fmt.Sprintf("$$timeshot$$%d/resize/size%dx%d/formatPNG/autorotate", 22, size.Width/5, size.Height/5),
						}
					case "pdf":
						actions = []string{
							"/master",
						}
					}
					for _, action := range actions {
						path := fmt.Sprintf("%s%s", strings.TrimPrefix(item.URI, "mediaserver:"), action)
						if err := cacheItem(conf.MediaserverBase, path, filepath.Join(conf.DataDir, "mediaserver"), logger); err != nil {
							logger.Panic().Err(err).Msgf("cannot cache item %s", path)
						}
					}
				}
			}
		}
	}
}

var mediaserverRegexp = regexp.MustCompile(`^([^/]+)/([^/]+)/([^/]+)(/.+)?$`)

func Encrypt(src []byte) ([]byte, error) {
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	cfb := cipher.NewCFBEncrypter(block, iv)
	cipherText := make([]byte, len(src))
	cfb.XORKeyStream(cipherText, src)
	return cipherText, nil
}

func Decrypt(src []byte) ([]byte, error) {
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	cfb := cipher.NewCFBDecrypter(block, iv)
	plainText := make([]byte, len(src))
	cfb.XORKeyStream(plainText, src)
	return plainText, nil
}
func cacheItem(mediaserverBase, uStr string, dir string, logger zLogger.ZLogger) error {
	// logger.Info().Msgf("caching %s/%s", mediaserverBase, uStr)
	parts := mediaserverRegexp.FindStringSubmatch(uStr)
	if parts == nil {
		return errors.New(fmt.Sprintf("invalid path: %s", uStr))
	}
	params := strings.Split(strings.Trim(strings.ToLower(parts[4]), "/"), "/")
	slices.Sort(params)
	uStr = strings.TrimRight(fmt.Sprintf("%s/%s/%s/%s", parts[1], parts[2], parts[3], strings.Join(params, "/")), "/")

	shaSum := fmt.Sprintf("%x", sha1.Sum([]byte(uStr)))
	sha512Sum := fmt.Sprintf("%x", sha512.Sum512([]byte(uStr)))
	_ = sha512Sum
	cacheFileOld := filepath.Join(dir, "cache", string(shaSum[0]), shaSum)
	cacheFile := filepath.Join(dir, "cache", string(shaSum[0]), string(shaSum[1]), shaSum)
	if _, err := os.Stat(cacheFileOld); err == nil {
		if err := os.MkdirAll(filepath.Dir(cacheFile), 0755); err != nil && !errors.Is(err, fs.ErrExist) {
			return errors.Wrapf(err, "cannot create cache folder %s", filepath.Dir(cacheFile))
		}
		if err := os.Rename(cacheFileOld, cacheFile); err != nil {
			return errors.Wrapf(err, "cannot rename %s to %s", cacheFileOld, cacheFile)
		}
		logger.Info().Msgf("cache file moved: %s -> %s", cacheFileOld, cacheFile)
		if _, err := os.Stat(cacheFileOld + ".mime"); err == nil {
			if err := os.Rename(cacheFileOld+".mime", cacheFile+".mime"); err != nil {
				return errors.Wrapf(err, "cannot rename %s to %s", cacheFileOld+".mime", cacheFile+".mime")
			}
			logger.Info().Msgf("cache file moved: %s -> %s", cacheFileOld+".mime", cacheFile+".mime")
		}
	}
	if fi, err := os.Stat(cacheFile); err == nil && fi.Size() > 0 {
		if fi2, err2 := os.Stat(cacheFile + ".mime"); err2 == nil && fi2.Size() > 0 {
			logger.Info().Msgf("cache hit: %s", cacheFile)
			return nil
		}
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
	urlStr := fmt.Sprintf("%s/%s", mediaserverBase, uStr)
	logger.Info().Msgf("loading %s - %s", shaSum, urlStr)
	resp, err := client.Get(urlStr)
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
	mime := resp.Header.Get("Content-Type")
	if strings.HasPrefix(mime, "text/html") {
		os.Remove(cacheFile)
		logger.Error().Msgf("invalid mime type for %s: %s", uStr, mime)
		return nil
		//return errors.New(fmt.Sprintf("invalid mime type for %s: %s", uStr, mime))
	}
	if err := os.WriteFile(cacheFile+".mime", []byte(mime), 0644); err != nil {
		os.Remove(cacheFile)
		return errors.Wrapf(err, "cannot write mime file %s", cacheFile+".mime")
	}
	return nil
}
