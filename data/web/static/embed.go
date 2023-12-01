package static

import "embed"

//go:embed bootstrap/css/bootstrap.min.css bootstrap/css/bootstrap.min.css.map
//go:embed bootstrap/js/bootstrap.bundle.min.js bootstrap/js/bootstrap.bundle.min.js.map
//go:embed bootstrap-icons/font/bootstrap-icons.min.css bootstrap-icons/font/fonts/bootstrap-icons.woff2 bootstrap-icons/font/fonts/bootstrap-icons.woff
//go:embed css/blog.css
//go:embed img/frame.svg img/histories.png img/revolving.png
//go:embed js/d3.js js/d3bubble.js
var FS embed.FS
