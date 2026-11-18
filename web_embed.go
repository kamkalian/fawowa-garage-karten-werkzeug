package main

import (
	"embed"
	"io/fs"
)

//go:embed web/index.html
var uiEingebettet embed.FS

// uiDateien: Wurzel ist der Ordner web/, damit index.html unter diesem Namen zu finden ist
var uiDateien, _ = fs.Sub(uiEingebettet, "web")
