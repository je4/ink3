package main

import (
	"fmt"
	"io/fs"
	"net"
	"os"

	"emperror.dev/errors"
	"github.com/BurntSushi/toml"
	"github.com/je4/ink3/v2/pkg/server"
	configutil "github.com/je4/utils/v2/pkg/config"
)

type network struct {
	net.IPNet
}

func (n *network) UnmarshalText(text []byte) error {
	_, net, err := net.ParseCIDR(string(text))
	if err != nil {
		return err
	}
	if net == nil {
		return fmt.Errorf("no network - %s", string(text))
	}
	n.IPNet = *net
	return nil
}

type Network struct {
	Group    string    `toml:"group"`
	Networks []network `toml:"networks"`
}

type RevcatConfig struct {
	Endpoint string               `toml:"endpoint"`
	Insecure bool                 `toml:"insecure"`
	Apikey   configutil.EnvString `toml:"apikey"`
}

type Directus struct {
	BaseUrl   string               `toml:"baseurl"`
	Token     configutil.EnvString `toml:"token"`
	CacheTime configutil.Duration  `toml:"cachetime"`
	CatalogID int                  `toml:"catalogid"`
}

type LocaleConfig struct {
	Default   string   `toml:"default"`
	Folder    string   `toml:"folder"`
	Available []string `toml:"available"`
}

type AuthConfig struct {
	User     string `toml:"user"`
	Password string `toml:"password"`
}

type Login struct {
	JWTKey       configutil.EnvString `toml:"jwtkey"`
	JWTAlg       []string             `toml:"jwtalg"`
	LinkTokenExp configutil.Duration  `toml:"linktokenexp"`
	URL          string               `toml:"url"`
	Issuer       string               `toml:"issuer"`
}

type RevCatFrontConfig struct {
	Name                string                  `toml:"name"`
	LocalAddr           string                  `toml:"localaddr"`
	ExternalAddr        string                  `toml:"externaladdr"`
	SearchAddr          string                  `toml:"searchaddr"`
	DetailAddr          string                  `toml:"detailaddr"`
	FacetInclude        []string                `toml:"facetinclude"`
	FacetExclude        []string                `toml:"facetexclude"`
	TLSCert             string                  `toml:"tlscert"`
	TLSKey              string                  `toml:"tlskey"`
	ProtoHTTP           bool                    `toml:"protohttp"`
	Auth                []*AuthConfig           `toml:"auth"`
	OpenAIApiKey        configutil.EnvString    `toml:"openaiapikey"`
	Templates           string                  `toml:"templates"`
	StaticFiles         string                  `toml:"staticfiles"`
	Locale              LocaleConfig            `toml:"locale"`
	LogFile             string                  `toml:"logfile"`
	LogLevel            string                  `toml:"loglevel"`
	Revcat              RevcatConfig            `toml:"revcat"`
	Directus            Directus                `toml:"directus"`
	ZoomOnly            bool                    `toml:"zoomonly"`
	MediaserverBase     string                  `toml:"mediaserverbase"`
	MediaserverTokenExp configutil.Duration     `toml:"mediaservertokenexp"`
	MediaserverKey      configutil.EnvString    `toml:"mediaserverkey"`
	DataDir             string                  `toml:"datadir"`
	Collections         []*server.CollFacetType `toml:"collections"`
	FieldMapping        map[string]string       `toml:"fieldmapping"`
	JWTKey              configutil.EnvString    `toml:"jwtkey"`
	JWTAlg              string                  `toml:"jwtalg"`
	Login               Login                   `toml:"login"`
	Locations           []Network               `toml:"locations"`
	Mode                string                  `toml:"mode"`
}

func LoadRevCatFrontConfig(fSys fs.FS, fp string, conf *RevCatFrontConfig) error {
	if _, err := fs.Stat(fSys, fp); err != nil {
		path, err := os.Getwd()
		if err != nil {
			return errors.Wrap(err, "cannot get current working directory")
		}
		fSys = os.DirFS(path)
		fp = "revcatfront.toml"
	}
	data, err := fs.ReadFile(fSys, fp)
	if err != nil {
		return errors.Wrapf(err, "cannot read file [%v] %s", fSys, fp)
	}
	_, err = toml.Decode(string(data), conf)
	if err != nil {
		return errors.Wrapf(err, "error loading config file %v", fp)
	}
	return nil
}
