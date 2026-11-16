package main

import (
	_ "embed"
	"html/template"
	"path"
)

//go:embed page.tmpl
var pageSource string

var pageTemplate = template.Must(template.New("page").Parse(pageSource))

const homeLabel = "Home"

type view struct {
	Title     string
	Desc      string
	Root      string
	Markdown  string
	Source    string
	BackHref  string
	BackLabel string
	Body      template.HTML
}

func (s *site) view(p *page) view {
	v := view{
		Title:    p.title,
		Desc:     p.desc,
		Root:     href(p.url, "/"),
		Markdown: path.Base(markdownURL(p)),
		Source:   s.repo + "/blob/main/" + p.src,
		Body:     template.HTML(p.body),
	}
	v.BackHref, v.BackLabel = s.back(p)
	return v
}

func (s *site) back(p *page) (string, string) {
	dir := path.Dir(p.src)
	if path.Base(p.src) == "README.md" {
		dir = path.Dir(dir)
	}
	if index := s.pages[dir+"/README.md"]; index != nil {
		return href(p.url, "/"+siteLink(index.url)), index.title
	}
	return href(p.url, "/"), homeLabel
}
