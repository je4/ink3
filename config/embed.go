package config

import (
	"embed"
)

//go:embed revcatfront.toml active.de.toml active.it.toml active.en.toml active.fr.toml
var ConfigFS embed.FS
