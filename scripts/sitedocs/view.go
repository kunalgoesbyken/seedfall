package main

import (
	_ "embed"
	"html/template"
	"path"
	"strings"
)

//go:embed page.tmpl
var pageSource string

var pageTemplate = template.Must(template.New("page").Parse(pageSource))

type view struct {
	Title      string
	Desc       string
	Root       string
	Docs       string
	Repo       string
	Markdown   string
	Source     string
	CrumbHref  string
	CrumbLabel string
	Groups     []groupView
	Body       template.HTML
}

type groupView struct {
	Name  string
	Items []itemView
}

type itemView struct {
	Label   string
	Href    string
	Current bool
}

func (s *site) view(p *page) view {
	v := view{
		Title:    p.title,
		Desc:     p.desc,
		Root:     href(p.url, "/"),
		Docs:     href(p.url, "/docs/"),
		Repo:     s.repo,
		Markdown: path.Base(markdownURL(p)),
		Source:   s.repo + "/blob/main/" + p.src,
		Groups:   s.groupViews(p),
		Body:     template.HTML(p.body),
	}
	v.CrumbHref, v.CrumbLabel = s.crumb(p)
	return v
}

func (s *site) groupViews(p *page) []groupView {
	current := "/" + siteLink(p.url)
	var out []groupView
	for _, g := range s.groups {
		gv := groupView{Name: g.Name}
		for _, it := range g.Items {
			gv.Items = append(gv.Items, itemView{it.Label, href(p.url, it.Target), pathOf(it.Target) == current})
		}
		if len(gv.Items) > 0 {
			out = append(out, gv)
		}
	}
	return out
}

func pathOf(target string) string {
	p, _, _ := strings.Cut(target, "#")
	return p
}

func (s *site) crumb(p *page) (string, string) {
	dir := path.Dir(p.src)
	if dir == "docs" || path.Base(p.url) == "index.html" {
		return "", ""
	}
	index := s.pages[dir+"/README.md"]
	if index == nil {
		return "", ""
	}
	return "./", index.title
}
