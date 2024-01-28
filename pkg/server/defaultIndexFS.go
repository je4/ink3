package server

import (
	"emperror.dev/errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
)

type DefaultIndexFS struct {
	fs        http.FileSystem
	indexFile string
}

func NewDefaultIndexFS(fs http.FileSystem, indexFile string) http.FileSystem {
	return &DefaultIndexFS{fs: fs, indexFile: indexFile}
}

func (d *DefaultIndexFS) Open(name string) (http.File, error) {
	if strings.HasSuffix(name, "/") {
		name = filepath.ToSlash(filepath.Join(name, d.indexFile))
	}
	f, err := d.fs.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			switch {
			case slices.Contains([]string{"/pdfjs/lib", "/pdfjs/lib/assets/fonts", "/pdfjs/lib/chunks", "/pdfjs/lib/i18n"}, filepath.ToSlash(filepath.Dir(name))):
				name = "/pdfjs/lib/ui/" + strings.TrimPrefix(name, "/pdfjs/lib/")
			case strings.HasPrefix(name, "/pdfjs/core/"):
				name = "/pdfjs/lib/core/" + strings.TrimPrefix(name, "/pdfjs/core/")
			default:
				return nil, errors.Wrapf(err, "cannot open %s", name)
			}
			f, err = d.fs.Open(name)
			if err != nil {
				return nil, errors.Wrapf(err, "cannot open %s", name)
			}
		} else {
			return nil, errors.Wrapf(err, "cannot open %s", name)
		}
	}
	info, err := f.Stat()
	if err != nil {
		return nil, errors.Wrapf(err, "cannot stat %s", name)
	}
	if info.IsDir() {
		f.Close()
		name = filepath.ToSlash(filepath.Join(name, d.indexFile))
		f, err = d.fs.Open(name)
		if err != nil {
			return nil, errors.Wrapf(err, "cannot open %s", name)
		}
	}
	return f, nil
}
