package templates

import "embed"

//go:embed index.gohtml search_grid.gohtml head.gohtml nav.gohtml footer.gohtml detail_text.de.gotmpl
var FS embed.FS
