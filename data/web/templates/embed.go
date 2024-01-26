package templates

import "embed"

//go:embed index.gohtml search_grid.gohtml head.gohtml nav.gohtml footer.gohtml detail_text.gotmpl detail.gohtml
var FS embed.FS
