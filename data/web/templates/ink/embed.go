package ink

import "embed"

//go:embed index.gohtml search_grid.gohtml head.gohtml nav.gohtml
//go:embed footer.gohtml detail_text.gotmpl detail.gohtml detail_image.gohtml
//go:embed detail_pdf_pdfjs.gohtml detail_video.gohtml detail_audio.gohtml zoom.gohtml detail_pdf_dflip.gohtml detail_verovio.gohtml detail_webrecorder.gohtml detail_epub_foliate.gohtml
//go:embed impressum.gohtml kontakt.gohtml
//go:embed foliatejsviewer.gohtml
var FS embed.FS
