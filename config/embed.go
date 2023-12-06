package config

import (
	"embed"
)

//go:embed revcatfront.toml active.??.toml
var ConfigFS embed.FS
