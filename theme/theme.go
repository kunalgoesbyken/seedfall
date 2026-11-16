package theme

import (
	_ "embed"
	"html/template"
)

//go:embed theme.css
var stylesheet string

func Stylesheet() template.CSS { return template.CSS(stylesheet) }
