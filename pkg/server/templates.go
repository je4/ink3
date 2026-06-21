package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	tmpl "text/template"

	"emperror.dev/errors"
	"github.com/Masterminds/sprig/v3"
	"github.com/go-git/go-git/v5/utils/ioutil"
	"github.com/gosimple/slug"
	"github.com/je4/revcat/v2/tools/client"
	"github.com/je4/zsearch/v2/pkg/translate"
	"github.com/nicksnyder/go-i18n/v2/i18n"
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

// funcMap returns the map of template functions.
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

// refreshTemplateFiles rescans the template directory for .gohtml and .gotmpl files.
func (ctrl *Controller) refreshTemplateFiles() error {
	ctrl.templateFiles = []string{}
	return fs.WalkDir(ctrl.templateFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".gohtml" || ext == ".gotmpl" {
				ctrl.templateFiles = append(ctrl.templateFiles, path)
			}
		}
		return nil
	})
}

// getTemplatesByPrefix returns a list of template files that start with a given prefix or contain no underscore.
func (ctrl *Controller) getTemplatesByPrefix(prefix string, exclude ...string) []string {
	var files []string
	for _, file := range ctrl.templateFiles {
		if slices.Contains(exclude, file) {
			continue
		}
		// Regel: Dateien, die mit dem Präfix beginnen ODER keinen Unterstrich enthalten
		// (Kein Unterstrich schließt head.gohtml, nav.gohtml, footer.gohtml etc. ein)
		if strings.HasPrefix(file, prefix) || !strings.Contains(file, "_") {
			files = append(files, file)
		}
	}
	return files
}

// loadHTMLTemplate parses and loads an HTML template from the filesystem.
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

// loadTextTemplate parses and loads a text template from the filesystem.
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
