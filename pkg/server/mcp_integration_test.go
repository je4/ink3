package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"emperror.dev/errors"
	"github.com/BurntSushi/toml"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gqlgo/gqlgenc/clientv2"
	"github.com/je4/ink3/v2/config"
	"github.com/je4/revcat/v2/tools/client"
	configutil "github.com/je4/utils/v2/pkg/config"
	"github.com/je4/utils/v2/pkg/zLogger"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/rs/zerolog"
	"golang.org/x/text/language"
)

// Integration config structures mirroring TOML config
type integrationNetwork struct {
	net.IPNet
}

func (n *integrationNetwork) UnmarshalText(text []byte) error {
	_, parsedNet, err := net.ParseCIDR(string(text))
	if err != nil {
		return err
	}
	if parsedNet == nil {
		return fmt.Errorf("no network - %s", string(text))
	}
	n.IPNet = *parsedNet
	return nil
}

type integrationNetworkGroup struct {
	Group    string               `toml:"group"`
	Networks []integrationNetwork `toml:"networks"`
}

type integrationRevcatConfig struct {
	Endpoint string               `toml:"endpoint"`
	Insecure bool                 `toml:"insecure"`
	Apikey   configutil.EnvString `toml:"apikey"`
}

type integrationDirectusConfig struct {
	BaseUrl   string               `toml:"baseurl"`
	Token     configutil.EnvString `toml:"token"`
	CacheTime configutil.Duration  `toml:"cachetime"`
	CatalogID int                  `toml:"catalogid"`
}

type integrationLocaleConfig struct {
	Default   string   `toml:"default"`
	Folder    string   `toml:"folder"`
	Available []string `toml:"available"`
}

type integrationAuthConfig struct {
	User     string `toml:"user"`
	Password string `toml:"password"`
}

type integrationLoginConfig struct {
	JWTKey       configutil.EnvString `toml:"jwtkey"`
	JWTAlg       []string             `toml:"jwtalg"`
	LinkTokenExp configutil.Duration  `toml:"linktokenexp"`
	URL          string               `toml:"url"`
	Issuer       string               `toml:"issuer"`
}

type integrationConfig struct {
	Name                string                    `toml:"name"`
	LocalAddr           string                    `toml:"localaddr"`
	ExternalAddr        string                    `toml:"externaladdr"`
	SearchAddr          string                    `toml:"searchaddr"`
	DetailAddr          string                    `toml:"detailaddr"`
	FacetInclude        []string                  `toml:"facetinclude"`
	FacetExclude        []string                  `toml:"facetexclude"`
	BaseFilter          string                    `toml:"basefilter"`
	TLSCert             string                    `toml:"tlscert"`
	TLSKey              string                    `toml:"tlskey"`
	ProtoHTTP           bool                      `toml:"protohttp"`
	Auth                []*integrationAuthConfig  `toml:"auth"`
	OpenAIApiKey        configutil.EnvString      `toml:"openaiapikey"`
	Templates           string                    `toml:"templates"`
	StaticFiles         string                    `toml:"staticfiles"`
	PagesFiles          string                    `toml:"pagesfiles"`
	Locale              integrationLocaleConfig   `toml:"locale"`
	LogFile             string                    `toml:"logfile"`
	LogLevel            string                    `toml:"loglevel"`
	Revcat              integrationRevcatConfig   `toml:"revcat"`
	Directus            integrationDirectusConfig `toml:"directus"`
	ZoomOnly            bool                      `toml:"zoomonly"`
	MediaserverBase     string                    `toml:"mediaserverbase"`
	MediaserverTokenExp configutil.Duration       `toml:"mediaservertokenexp"`
	MediaserverKey      configutil.EnvString      `toml:"mediaserverkey"`
	DataDir             string                    `toml:"datadir"`
	Collections         []*CollFacetType          `toml:"collections"`
	Catalogs            []*CollFacetType          `toml:"catalogs"`
	Medias              []*CollFacetType          `toml:"medias"`
	Estates             []*CollFacetType          `toml:"estates"`
	Topics              []*CollFacetType          `toml:"topics"`
	FieldMapping        map[string]string         `toml:"fieldmapping"`
	JWTKey              configutil.EnvString      `toml:"jwtkey"`
	JWTAlg              string                    `toml:"jwtalg"`
	Login               integrationLoginConfig    `toml:"login"`
	Locations           []integrationNetworkGroup `toml:"locations"`
	Mode                string                    `toml:"mode"`
}

type integrationGroupClaims struct {
	jwt.RegisteredClaims
	Groups string `json:"groups"`
}

// loadDotEnv loads key=value pairs from a .env file and sets them into os environment.
func loadDotEnv(filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Remove wrapping quotes if present
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if key != "" {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

// findFirstExisting attempts to locate a file from a list of candidate relative/absolute paths.
func findFirstExisting(candidates ...string) string {
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// loadIntegrationConfig loads configuration from the specified TOML path or default candidates.
func loadIntegrationConfig(customPath string) (*integrationConfig, error) {
	candidates := []string{}
	if customPath != "" {
		candidates = append(candidates, customPath)
	}
	candidates = append(candidates,
		"config/revcatfront_ink3_dev.toml",
		"../../config/revcatfront_ink3_dev.toml",
		"../config/revcatfront_ink3_dev.toml",
	)

	configPath := findFirstExisting(candidates...)
	if configPath == "" {
		return nil, os.ErrNotExist
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read config file %s", configPath)
	}

	conf := &integrationConfig{
		LogLevel:     "DEBUG",
		LocalAddr:    "localhost:8445",
		ExternalAddr: "https://localhost:8445",
		Mode:         "auto",
		BaseFilter:   "[]",
	}

	if _, err := toml.Decode(string(data), conf); err != nil {
		return nil, errors.Wrapf(err, "failed to decode TOML config from %s", configPath)
	}

	return conf, nil
}

// checkBackendAvailability verifies if the RevCat GraphQL endpoint is reachable.
func checkBackendAvailability(endpoint string, insecure bool) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL %q: %w", endpoint, err)
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	// First quick TCP dial probe
	conn, err := net.DialTimeout("tcp", host, 3*time.Second)
	if err != nil {
		return fmt.Errorf("cannot connect to backend %s: %w", host, err)
	}
	conn.Close()

	// HTTP probe
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   4 * time.Second,
	}

	resp, err := client.Get(endpoint)
	if err != nil {
		// Even if GET returns 405 Method Not Allowed or 400 Bad Request, the server is alive
		return fmt.Errorf("backend HTTP probe failed on %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	return nil
}

// integrationHarness holds initialized test components.
type integrationHarness struct {
	Controller *Controller
	Server     *httptest.Server
	Session    *mcp.ClientSession
	Config     *integrationConfig
}

var (
	sharedHarness     *integrationHarness
	sharedHarnessErr  error
	sharedHarnessOnce sync.Once
)

// setupIntegrationHarness initializes the environment, configuration, controller, test server, and MCP client.
func setupIntegrationHarness(t *testing.T) *integrationHarness {
	t.Helper()

	envPath := findFirstExisting(
		os.Getenv("ENV_FILE"),
		".env",
		"../../.env",
		"../.env",
	)
	if envPath == "" {
		t.Skip("skipping integration test: .env file not found")
	}

	if err := loadDotEnv(envPath); err != nil {
		t.Skipf("skipping integration test: failed to load .env from %s: %v", envPath, err)
	}

	conf, err := loadIntegrationConfig(os.Getenv("CONFIG_FILE"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skip("skipping integration test: config/revcatfront_ink3_dev.toml not found")
		}
		t.Fatalf("failed to load integration configuration: %v", err)
	}

	// Probe backend
	if err := checkBackendAvailability(conf.Revcat.Endpoint, conf.Revcat.Insecure); err != nil {
		t.Skipf("skipping integration test: RevCat GraphQL backend %s is unreachable: %v", conf.Revcat.Endpoint, err)
	}

	sharedHarnessOnce.Do(func() {
		sharedHarness, sharedHarnessErr = buildIntegrationHarness(conf)
	})

	if sharedHarnessErr != nil {
		t.Fatalf("failed to initialize integration harness: %v", sharedHarnessErr)
	}

	return sharedHarness
}

// buildIntegrationHarness constructs controller and MCP client session.
func buildIntegrationHarness(conf *integrationConfig) (*integrationHarness, error) {
	out := zerolog.NewConsoleWriter(func(w *zerolog.ConsoleWriter) {
		w.Out = os.Stdout
		w.TimeFormat = time.RFC3339
	})
	_logger := zerolog.New(out).With().Timestamp().Logger()
	_logger.Level(zLogger.LogLevel(conf.LogLevel))
	var logger zLogger.ZLogger = &_logger

	var localeFS fs.FS
	if conf.Locale.Folder != "" {
		if fi, err := os.Stat(conf.Locale.Folder); err == nil && fi.IsDir() {
			localeFS = os.DirFS(conf.Locale.Folder)
		}
	}
	if localeFS == nil {
		localeFS = config.ConfigFS
	}

	glang, err := language.Parse(conf.Locale.Default)
	if err != nil {
		glang = language.German
	}
	bundle := i18n.NewBundle(glang)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	for _, lang := range conf.Locale.Available {
		localeFile := fmt.Sprintf("active.%s.toml", lang)
		if _, err := fs.Stat(localeFS, localeFile); err == nil {
			_, _ = bundle.LoadMessageFileFS(localeFS, localeFile)
		}
	}

	var templateFS fs.FS
	if conf.Templates != "" {
		if fi, err := os.Stat(conf.Templates); err == nil && fi.IsDir() {
			templateFS = os.DirFS(conf.Templates)
		}
	}
	if templateFS == nil {
		templateFS = config.ConfigFS
	}

	var staticFS fs.FS
	if conf.StaticFiles != "" {
		if fi, err := os.Stat(conf.StaticFiles); err == nil && fi.IsDir() {
			staticFS = os.DirFS(conf.StaticFiles)
		}
	}
	if staticFS == nil {
		staticFS = config.ConfigFS
	}

	var pagesFS fs.FS
	if conf.PagesFiles != "" {
		if fi, err := os.Stat(conf.PagesFiles); err == nil && fi.IsDir() {
			pagesFS = os.DirFS(conf.PagesFiles)
		}
	}

	var dataFS fs.FS
	if conf.DataDir != "" {
		if fi, err := os.Stat(conf.DataDir); err == nil && fi.IsDir() {
			dataFS = os.DirFS(conf.DataDir)
		}
	}

	// RevCat GraphQL Client with JWT Auth
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: conf.Revcat.Insecure},
	}
	httpClient := &http.Client{Transport: tr}

	revcatClient := client.NewClient(
		httpClient,
		conf.Revcat.Endpoint,
		nil,
		func(ctx context.Context, req *http.Request, gqlInfo *clientv2.GQLRequestInfo, res interface{}, next clientv2.RequestInterceptorFunc) error {
			userAny := ctx.Value("user")
			groups := []string{"global/guest"}
			if user, ok := userAny.(*User); ok {
				groups = user.Groups
			}
			bearer := fmt.Sprintf("Bearer %s", conf.Revcat.Apikey)
			claims := &integrationGroupClaims{
				RegisteredClaims: jwt.RegisteredClaims{
					Subject:   "revcatfront",
					Issuer:    "revcatfront",
					ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute * 5)),
					IssuedAt:  jwt.NewNumericDate(time.Now()),
				},
				Groups: strings.Join(groups, ";"),
			}
			token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
			tokenString, err := token.SignedString([]byte(conf.JWTKey))
			if err != nil {
				req.Header.Set("Authorization", bearer)
				return next(ctx, req, gqlInfo, res)
			}
			req.Header.Set("Authorization", bearer+"."+tokenString)
			return next(ctx, req, gqlInfo, res)
		},
	)

	var authConfig map[string]string
	if len(conf.Auth) > 0 {
		authConfig = make(map[string]string)
		for _, a := range conf.Auth {
			authConfig[a.User] = a.Password
		}
	}

	locations := make(map[string][]net.IPNet)
	for _, loc := range conf.Locations {
		locations[loc.Group] = []net.IPNet{}
		for _, netw := range loc.Networks {
			locations[loc.Group] = append(locations[loc.Group], netw.IPNet)
		}
	}

	var baseFilter []*client.InFilter
	if conf.BaseFilter != "" {
		_ = json.Unmarshal([]byte(conf.BaseFilter), &baseFilter)
	}

	ctrl, err := NewController(
		conf.Name,
		conf.LocalAddr,
		conf.ExternalAddr,
		conf.SearchAddr,
		conf.DetailAddr,
		conf.ProtoHTTP,
		authConfig,
		nil,
		templateFS,
		staticFS,
		dataFS,
		pagesFS,
		revcatClient,
		nil,
		conf.MediaserverBase,
		string(conf.MediaserverKey),
		time.Duration(conf.MediaserverTokenExp),
		bundle,
		conf.Collections,
		conf.Catalogs,
		conf.Medias,
		conf.Estates,
		conf.Topics,
		conf.FieldMapping,
		nil,
		false,
		conf.ZoomOnly,
		conf.Login.URL,
		conf.Login.Issuer,
		string(conf.Login.JWTKey),
		conf.Login.JWTAlg,
		locations,
		conf.FacetInclude,
		conf.FacetExclude,
		baseFilter,
		conf.Mode,
		logger,
	)
	if err != nil {
		return nil, errors.Wrap(err, "cannot create controller")
	}

	// Create httptest.Server with ctrl.srv.Handler
	ts := httptest.NewServer(ctrl.srv.Handler)

	// Create MCP Client
	clientTransport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp",
	}
	mcpClient := mcp.NewClient(&mcp.Implementation{
		Name:    "integration-test-client",
		Version: "1.0.0",
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := mcpClient.Connect(ctx, clientTransport, nil)
	if err != nil {
		ts.Close()
		return nil, errors.Wrap(err, "failed to connect MCP client session")
	}

	return &integrationHarness{
		Controller: ctrl,
		Server:     ts,
		Session:    session,
		Config:     conf,
	}, nil
}

// Helper to call MCP search tool and unmarshal SearchResult
func callMCPSearch(t *testing.T, session *mcp.ClientSession, args map[string]any) (*SearchResult, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("MCP CallTool 'search' failed: %v", err)
	}
	if res.IsError {
		var errText string
		for _, c := range res.Content {
			if txt, ok := c.(*mcp.TextContent); ok {
				errText += txt.Text + "\n"
			}
		}
		t.Fatalf("MCP CallTool 'search' returned tool error: %s", errText)
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal MCP structured content: %v", err)
	}

	var searchResult SearchResult
	if err := json.Unmarshal(raw, &searchResult); err != nil {
		t.Fatalf("failed to unmarshal SearchResult: %v, raw: %s", err, string(raw))
	}

	var mdText string
	for _, c := range res.Content {
		if txt, ok := c.(*mcp.TextContent); ok {
			mdText += txt.Text
		}
	}

	return &searchResult, mdText
}

// Helper to assert parity between MCP SearchResult and Controller baseline search
func assertSearchParity(t *testing.T, mcpResult *SearchResult, baselineResult *SearchResult, description string) {
	t.Helper()

	if mcpResult == nil || baselineResult == nil {
		t.Fatalf("[%s] nil search result: mcp=%v baseline=%v", description, mcpResult, baselineResult)
	}

	if mcpResult.TotalCount != baselineResult.TotalCount {
		t.Errorf("[%s] TotalCount mismatch: MCP=%d, Baseline=%d", description, mcpResult.TotalCount, baselineResult.TotalCount)
	}

	if len(mcpResult.Items) != len(baselineResult.Items) {
		t.Errorf("[%s] Items count mismatch: MCP=%d, Baseline=%d", description, len(mcpResult.Items), len(baselineResult.Items))
	}

	// Compare page info
	if (mcpResult.PageInfo == nil) != (baselineResult.PageInfo == nil) {
		t.Errorf("[%s] PageInfo presence mismatch: MCP=%v, Baseline=%v", description, mcpResult.PageInfo, baselineResult.PageInfo)
	} else if mcpResult.PageInfo != nil && baselineResult.PageInfo != nil {
		if mcpResult.PageInfo.HasNextPage != baselineResult.PageInfo.HasNextPage {
			t.Errorf("[%s] PageInfo.HasNextPage mismatch: MCP=%v, Baseline=%v", description, mcpResult.PageInfo.HasNextPage, baselineResult.PageInfo.HasNextPage)
		}
		if mcpResult.PageInfo.EndCursor != baselineResult.PageInfo.EndCursor {
			t.Errorf("[%s] PageInfo.EndCursor mismatch: MCP=%q, Baseline=%q", description, mcpResult.PageInfo.EndCursor, baselineResult.PageInfo.EndCursor)
		}
	}

	limit := min(len(mcpResult.Items), len(baselineResult.Items))
	for i := 0; i < limit; i++ {
		mcpItem := mcpResult.Items[i]
		baseItem := baselineResult.Items[i]

		if mcpItem.Signature != baseItem.Signature {
			t.Errorf("[%s] Item[%d] Signature mismatch: MCP=%q, Baseline=%q", description, i, mcpItem.Signature, baseItem.Signature)
		}
		if mcpItem.Title != baseItem.Title {
			t.Errorf("[%s] Item[%d] Title mismatch: MCP=%q, Baseline=%q", description, i, mcpItem.Title, baseItem.Title)
		}
		if mcpItem.Date != baseItem.Date {
			t.Errorf("[%s] Item[%d] Date mismatch: MCP=%q, Baseline=%q", description, i, mcpItem.Date, baseItem.Date)
		}
		if mcpItem.Type != baseItem.Type {
			t.Errorf("[%s] Item[%d] Type mismatch: MCP=%q, Baseline=%q", description, i, mcpItem.Type, baseItem.Type)
		}
		if mcpItem.Url != baseItem.Url {
			t.Errorf("[%s] Item[%d] Url mismatch: MCP=%q, Baseline=%q", description, i, mcpItem.Url, baseItem.Url)
		}
		if !reflect.DeepEqual(mcpItem.Persons, baseItem.Persons) {
			t.Errorf("[%s] Item[%d] Persons mismatch: MCP=%v, Baseline=%v", description, i, mcpItem.Persons, baseItem.Persons)
		}
	}
}

// TestIntegration_MCPSearch_FreeText tests free text search queries for result parity.
func TestIntegration_MCPSearch_FreeText(t *testing.T) {
	harness := setupIntegrationHarness(t)

	queries := []string{
		"Performance",
		"Video",
		"Basel",
		"",
	}

	for _, q := range queries {
		t.Run(fmt.Sprintf("query_%s", q), func(t *testing.T) {
			args := SearchArgs{
				Query:    q,
				PageSize: 10,
			}

			// Baseline direct call
			baseResult, _, err := harness.Controller.search(t.Context(), args)
			if err != nil {
				t.Fatalf("Controller.search failed: %v", err)
			}

			// MCP CallTool
			mcpArgs := map[string]any{
				"query":    q,
				"pageSize": 10,
			}
			mcpResult, mdText := callMCPSearch(t, harness.Session, mcpArgs)

			assertSearchParity(t, mcpResult, baseResult, fmt.Sprintf("FreeText q=%q", q))

			if baseResult.TotalCount > 0 && mdText == "" {
				t.Errorf("Expected markdown text for query %q, got empty", q)
			}
		})
	}
}

// TestIntegration_MCPSearch_CollectionFilter tests collection-scoped searches.
func TestIntegration_MCPSearch_CollectionFilter(t *testing.T) {
	harness := setupIntegrationHarness(t)

	colls := harness.Controller.getCollections()
	if len(colls) == 0 {
		t.Skip("no collections configured in controller")
	}

	// Test first 2 collections
	testColls := colls
	if len(testColls) > 2 {
		testColls = testColls[:2]
	}

	for _, coll := range testColls {
		t.Run(fmt.Sprintf("coll_%s", coll.Title), func(t *testing.T) {
			args := SearchArgs{
				Collections: []string{coll.Title},
				PageSize:    10,
			}

			baseResult, _, err := harness.Controller.search(t.Context(), args)
			if err != nil {
				t.Fatalf("Controller.search failed: %v", err)
			}

			mcpArgs := map[string]any{
				"collections": []string{coll.Title},
				"pageSize":    10,
			}
			mcpResult, _ := callMCPSearch(t, harness.Session, mcpArgs)

			assertSearchParity(t, mcpResult, baseResult, fmt.Sprintf("Collection %q", coll.Title))
		})
	}
}

// TestIntegration_MCPSearch_TopicFilter tests topic-scoped searches.
func TestIntegration_MCPSearch_TopicFilter(t *testing.T) {
	harness := setupIntegrationHarness(t)

	topics := harness.Controller.getTopics()
	if len(topics) == 0 {
		t.Skip("no topics configured in controller")
	}

	testTopics := topics
	if len(testTopics) > 2 {
		testTopics = testTopics[:2]
	}

	for _, topic := range testTopics {
		t.Run(fmt.Sprintf("topic_%s", topic.Title), func(t *testing.T) {
			args := SearchArgs{
				Topics:   []string{topic.Title},
				PageSize: 10,
			}

			baseResult, _, err := harness.Controller.search(t.Context(), args)
			if err != nil {
				t.Fatalf("Controller.search failed: %v", err)
			}

			mcpArgs := map[string]any{
				"topics":   []string{topic.Title},
				"pageSize": 10,
			}
			mcpResult, _ := callMCPSearch(t, harness.Session, mcpArgs)

			assertSearchParity(t, mcpResult, baseResult, fmt.Sprintf("Topic %q", topic.Title))
		})
	}
}

// TestIntegration_MCPSearch_EstateFilter tests estate-scoped searches.
func TestIntegration_MCPSearch_EstateFilter(t *testing.T) {
	harness := setupIntegrationHarness(t)

	estates := harness.Controller.getEstates()
	if len(estates) == 0 {
		t.Skip("no estates configured in controller")
	}

	testEstates := estates
	if len(testEstates) > 2 {
		testEstates = testEstates[:2]
	}

	for _, estate := range testEstates {
		t.Run(fmt.Sprintf("estate_%s", estate.Title), func(t *testing.T) {
			args := SearchArgs{
				Estates:  []string{estate.Title},
				PageSize: 10,
			}

			baseResult, _, err := harness.Controller.search(t.Context(), args)
			if err != nil {
				t.Fatalf("Controller.search failed: %v", err)
			}

			mcpArgs := map[string]any{
				"estates":  []string{estate.Title},
				"pageSize": 10,
			}
			mcpResult, _ := callMCPSearch(t, harness.Session, mcpArgs)

			assertSearchParity(t, mcpResult, baseResult, fmt.Sprintf("Estate %q", estate.Title))
		})
	}
}

// TestIntegration_MCPSearch_Pagination tests pagination using offset (from) and cursor traversal.
func TestIntegration_MCPSearch_Pagination(t *testing.T) {
	harness := setupIntegrationHarness(t)

	t.Run("offset_pagination", func(t *testing.T) {
		// Page 1: from=0, pageSize=5
		args1 := SearchArgs{
			Query:    "Performance",
			From:     0,
			PageSize: 5,
		}
		baseRes1, _, err := harness.Controller.search(t.Context(), args1)
		if err != nil {
			t.Fatalf("Page 1 baseline search failed: %v", err)
		}

		mcpRes1, _ := callMCPSearch(t, harness.Session, map[string]any{
			"query":    "Performance",
			"from":     0,
			"pageSize": 5,
		})
		assertSearchParity(t, mcpRes1, baseRes1, "Page 1 (from=0, pageSize=5)")

		if baseRes1.TotalCount > 5 {
			// Page 2: from=5, pageSize=5
			args2 := SearchArgs{
				Query:    "Performance",
				From:     5,
				PageSize: 5,
			}
			baseRes2, _, err := harness.Controller.search(t.Context(), args2)
			if err != nil {
				t.Fatalf("Page 2 baseline search failed: %v", err)
			}

			mcpRes2, _ := callMCPSearch(t, harness.Session, map[string]any{
				"query":    "Performance",
				"from":     5,
				"pageSize": 5,
			})
			assertSearchParity(t, mcpRes2, baseRes2, "Page 2 (from=5, pageSize=5)")

			// Ensure page 1 and page 2 items don't overlap
			if len(mcpRes1.Items) > 0 && len(mcpRes2.Items) > 0 {
				if mcpRes1.Items[0].Signature == mcpRes2.Items[0].Signature {
					t.Errorf("Page 1 and Page 2 first item signature collision: %q", mcpRes1.Items[0].Signature)
				}
			}
		}
	})

	t.Run("cursor_pagination", func(t *testing.T) {
		mcpRes1, _ := callMCPSearch(t, harness.Session, map[string]any{
			"query":    "Performance",
			"pageSize": 5,
		})

		if mcpRes1.PageInfo != nil && mcpRes1.PageInfo.HasNextPage && mcpRes1.PageInfo.EndCursor != "" {
			mcpRes2, _ := callMCPSearch(t, harness.Session, map[string]any{
				"query":  "Performance",
				"cursor": mcpRes1.PageInfo.EndCursor,
			})

			baseRes2, _, err := harness.Controller.search(t.Context(), SearchArgs{
				Query:  "Performance",
				Cursor: mcpRes1.PageInfo.EndCursor,
			})
			if err != nil {
				t.Fatalf("Cursor baseline search failed: %v", err)
			}

			assertSearchParity(t, mcpRes2, baseRes2, "Cursor page 2")
		}
	})
}

// TestIntegration_MCPSearch_DetailConsistency tests that signatures from search results can be resolved via detail tool.
func TestIntegration_MCPSearch_DetailConsistency(t *testing.T) {
	harness := setupIntegrationHarness(t)

	mcpResult, _ := callMCPSearch(t, harness.Session, map[string]any{
		"query":    "Performance",
		"pageSize": 5,
	})

	if len(mcpResult.Items) == 0 {
		t.Skip("no search items found to test detail consistency")
	}

	firstItem := mcpResult.Items[0]

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	detailCallRes, err := harness.Session.CallTool(ctx, &mcp.CallToolParams{
		Name: "detail",
		Arguments: map[string]any{
			"signature": firstItem.Signature,
		},
	})
	if err != nil {
		t.Fatalf("MCP detail CallTool failed: %v", err)
	}

	raw, err := json.Marshal(detailCallRes.StructuredContent)
	if err != nil {
		t.Fatalf("failed to marshal detail StructuredContent: %v", err)
	}

	var detailResult DetailResult
	if err := json.Unmarshal(raw, &detailResult); err != nil {
		t.Fatalf("failed to unmarshal DetailResult: %v, raw: %s", err, string(raw))
	}

	if detailResult.Signature != firstItem.Signature {
		t.Errorf("Signature mismatch between search item and detail: search=%q, detail=%q", firstItem.Signature, detailResult.Signature)
	}
	if firstItem.Title != "" && detailResult.Title != "" && firstItem.Title != detailResult.Title {
		t.Errorf("Title mismatch between search item and detail: search=%q, detail=%q", firstItem.Title, detailResult.Title)
	}
}

// TestIntegration_MCPSearch_EdgeCases tests edge cases like non-existent filters or empty queries.
func TestIntegration_MCPSearch_EdgeCases(t *testing.T) {
	harness := setupIntegrationHarness(t)

	t.Run("non_existent_topic", func(t *testing.T) {
		mcpResult, mdText := callMCPSearch(t, harness.Session, map[string]any{
			"topics": []string{"__NonExistentTopicThatDoesNotExist__"},
		})
		if mcpResult.TotalCount != 0 {
			t.Errorf("Expected 0 results for non-existent topic, got %d", mcpResult.TotalCount)
		}
		if !strings.Contains(mdText, "Keine Ergebnisse") {
			t.Errorf("Expected markdown to contain 'Keine Ergebnisse', got: %s", mdText)
		}
	})

	t.Run("non_existent_collection", func(t *testing.T) {
		mcpResult, mdText := callMCPSearch(t, harness.Session, map[string]any{
			"collections": []string{"__NonExistentCollectionThatDoesNotExist__"},
		})
		if mcpResult.TotalCount != 0 {
			t.Errorf("Expected 0 results for non-existent collection, got %d", mcpResult.TotalCount)
		}
		if !strings.Contains(mdText, "Keine Ergebnisse") {
			t.Errorf("Expected markdown to contain 'Keine Ergebnisse', got: %s", mdText)
		}
	})

	t.Run("query_with_quotes_and_special_chars", func(t *testing.T) {
		mcpResult, _ := callMCPSearch(t, harness.Session, map[string]any{
			"query": "\"Performance\" AND NOT \"NonExistentTerm123456\"",
		})
		baseResult, _, err := harness.Controller.search(t.Context(), SearchArgs{
			Query: "\"Performance\" AND NOT \"NonExistentTerm123456\"",
		})
		if err != nil {
			t.Fatalf("baseline search failed: %v", err)
		}
		assertSearchParity(t, mcpResult, baseResult, "Query with quotes")
	})
}
