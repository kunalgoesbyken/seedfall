package theme

import (
	_ "embed"
	"html/template"
	"net/url"
)

//go:embed theme.css
var stylesheet string

//go:embed icon.svg
var icon string

func Stylesheet() template.CSS { return template.CSS(stylesheet) }

func Icon() template.URL { return template.URL("data:image/svg+xml," + url.PathEscape(icon)) }
