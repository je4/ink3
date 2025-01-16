package static

import "embed"

// go:embed pdfjs/lib/*
//
// go:embed templates/default-book-view.html
// go:embed sounds/*
// go:embed images/dark-loader.gif images/light-loader.gif
//
//go:embed js/search.js js/d3.js js/d3bubble.js
//go:embed bootstrap/css/bootstrap.min.css bootstrap/css/bootstrap.min.css.map
//go:embed bootstrap/js/bootstrap.bundle.min.js bootstrap/js/bootstrap.bundle.min.js.map
//go:embed bootstrap-icons/font/bootstrap-icons.min.css bootstrap-icons/font/fonts/bootstrap-icons.woff2
//go:embed bootstrap-icons/font/fonts/bootstrap-icons.woff
//go:embed css/csp.css css/ibm-plex-mono.css css/ibm-plex-sans-condensed.css css/ibm-plex-sans.css css/ibm-plex-serif.css
//go:embed fonts/ibm-plex-sans-v19-latin_latin-ext-regular.woff2 fonts/ibm-plex-sans-v19-latin_latin-ext-italic.woff2 fonts/FontAwesome.otf fonts/fontawesome*
//go:embed fonts/ibm-plex-sans-v19-latin_latin-ext-500.woff2 fonts/ibm-plex-sans-v19-latin_latin-ext-700.woff2
//go:embed fonts/ibm-plex-sans-v19-latin_latin-ext-600.woff2 fonts/ibm-plex-sans-v19-latin_latin-ext-600.ttf
//go:embed img/frame.svg img/frame0.png img/histories.png img/revolving.png img/7373.svg img/title_??_1024x117.png
//go:embed img/border*.png img/title_background*.png img/edge*.png img/image_mask.png img/120px-DeepL_logo.svg.png
//go:embed img/prev*.png img/next*.png img/?.png img/??.png img/hook.png img/line_medium.png img/line_short.png
//go:embed img/ki.png img/ki2.png img/lupe.png img/prev2.png img/index_bg2.jpg img/index_bg_dark.jpg img/index_bg_light.jpg
//go:embed img/lupe_black.png img/ki2_black.png img/coll_act.png img/wesen_behaelter/*_klein.png img/sdmllogo.png
//go:embed flag-icons/css/flag-icons.min.css flag-icons/flags/4x3/de.svg flag-icons/flags/4x3/gb-eng.svg
//go:embed flag-icons/flags/4x3/fr.svg flag-icons/flags/4x3/it.svg
//go:embed videojs/video-js.min.css videojs/video.min.js
//go:embed pdf.js/web/viewer.css pdf.js/web/viewer.html pdf.js/web/viewer.mjs pdf.js/web/images/* pdf.js/web/locale/*
//go:embed pdf.js/build/pdf.mjs pdf.js/web/cmaps/* pdf.js/build/pdf.worker.mjs
//go:embed openseadragon/images/* openseadragon/openseadragon.min.js openseadragon/openseadragon.min.js.map
//go:embed css/_csp_dark.scss css/_csp_light.scss css/ibm-plex*.css
//go:embed dflip/css/* dflip/js/* dflip/fonts/* dflip/images/* dflip/sound/*
var FS embed.FS
