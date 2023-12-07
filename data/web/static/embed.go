package static

import "embed"

//go:embed bootstrap/css/bootstrap.min.css bootstrap/css/bootstrap.min.css.map
//go:embed bootstrap/js/bootstrap.bundle.min.js bootstrap/js/bootstrap.bundle.min.js.map
//go:embed bootstrap-icons/font/bootstrap-icons.min.css bootstrap-icons/font/fonts/bootstrap-icons.woff2 bootstrap-icons/font/fonts/bootstrap-icons.woff
//go:embed css/csp.css css/ibm-plex-mono.css css/ibm-plex-sans-condensed.css css/ibm-plex-sans.css css/ibm-plex-serif.css
//go:embed img/frame.svg img/frame0.png img/histories.png img/revolving.png img/7373.svg img/title_de_1024x110.png img/title_en_1024x110.png img/title_fr_1024x110.png img/title_it_1024x110.png img/border*.png
//go:embed js/d3.js js/d3bubble.js
//go:embed flag-icons/css/flag-icons.min.css flag-icons/flags/4x3/de.svg flag-icons/flags/4x3/gb-eng.svg flag-icons/flags/4x3/fr.svg flag-icons/flags/4x3/it.svg
var FS embed.FS
