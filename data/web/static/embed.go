package static

import "embed"

//go:embed bootstrap/css/bootstrap.min.css bootstrap/css/bootstrap.min.css.map
//go:embed bootstrap/js/bootstrap.bundle.min.js bootstrap/js/bootstrap.bundle.min.js.map
//go:embed bootstrap-icons/font/bootstrap-icons.min.css bootstrap-icons/font/fonts/bootstrap-icons.woff2 bootstrap-icons/font/fonts/bootstrap-icons.woff
//go:embed css/csp.css css/ibm-plex-mono.css css/ibm-plex-sans-condensed.css css/ibm-plex-sans.css css/ibm-plex-serif.css
//go:embed fonts/ibm-plex-sans-v19-latin_latin-ext-regular.woff2 fonts/ibm-plex-sans-v19-latin_latin-ext-regular.woff2 fonts/ibm-plex-sans-v19-latin_latin-ext-italic.woff2 fonts/ibm-plex-sans-v19-latin_latin-ext-500.woff2 fonts/ibm-plex-sans-v19-latin_latin-ext-700.woff2
//go:embed img/frame.svg img/frame0.png img/histories.png img/revolving.png img/7373.svg img/title_??_1024x117.png img/border*.png img/title_background*.png img/edge*.png img/image_mask.png img/120px-DeepL_logo.svg.png img/prev*.png img/next*.png img/?.png img/??.png img/hook.png img/line_medium.png img/line_short.png
//go:embed js/d3.js js/d3bubble.js js/search.js js/chat.js
//go:embed flag-icons/css/flag-icons.min.css flag-icons/flags/4x3/de.svg flag-icons/flags/4x3/gb-eng.svg flag-icons/flags/4x3/fr.svg flag-icons/flags/4x3/it.svg
//go:embed videojs/video-js.min.css videojs/video.min.js
//go:embed pdfjs/lib/*
//go:embed openseadragon/images/* openseadragon/openseadragon.min.js openseadragon/openseadragon.min.js.map
var FS embed.FS
