package templates

import "embed"

//go:embed index.gohtml search_grid.gohtml head.gohtml nav.gohtml
var FS embed.FS
