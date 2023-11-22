package config

import (
	"embed"
)

//go:embed revcatfront.toml
var ConfigFS embed.FS
