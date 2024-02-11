package templates

import "embed"

//go:embed index.gohtml search_grid.gohtml head.gohtml nav.gohtml footer.gohtml detail_text.gotmpl detail.gohtml detail_image.gohtml detail_pdf.gohtml detail_video.gohtml detail_audio.gohtml zoom.gohtml
var FS embed.FS
