package main

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark/ast"
)

func (s *site) rewriteLinks(p *page) {
	ast.Walk(p.doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Link:
			v.Destination = []byte(href(p.url, s.target(p, string(v.Destination), "blob")))
		case *ast.Image:
			v.Destination = []byte(href(p.url, s.target(p, string(v.Destination), "raw")))
		}
		return ast.WalkContinue, nil
	})
}

func (s *site) target(from *page, dest, kind string) string {
	u, err := url.Parse(dest)
	if err != nil || u.IsAbs() || u.Host != "" {
		return dest
	}
	frag := ""
	if u.Fragment != "" {
		frag = "#" + u.Fragment
	}
	if u.Path == "" {
		s.checkFragment(from, from, dest, u.Fragment)
		return dest
	}
	target := path.Join(path.Dir(from.src), u.Path)
	if strings.HasPrefix(target, "../") {
		return dest
	}
	if p, ok := s.pages[target]; ok {
		s.checkFragment(from, p, dest, u.Fragment)
		return "/" + siteLink(p.url) + frag
	}
	if s.files[target] {
		return "/" + target + frag
	}
	return s.repo + "/" + kind + "/main/" + target + frag
}

func (s *site) checkFragment(from, to *page, dest, frag string) {
	if frag == "" || to.ids[frag] {
		return
	}
	s.problems[from.src+": link "+dest+" has no heading with id "+frag+" in "+to.src] = true
}

func siteLink(u string) string {
	if path.Base(u) == "index.html" {
		return strings.TrimSuffix(u, "index.html")
	}
	return u
}

func href(from, target string) string {
	site, ok := strings.CutPrefix(target, "/")
	if !ok {
		return target
	}
	p, frag, _ := strings.Cut(site, "#")
	if frag != "" {
		frag = "#" + frag
	}
	return relative(from, p) + frag
}

func relative(from, to string) string {
	r, err := filepath.Rel(path.Dir(from), to)
	if err != nil {
		return to
	}
	r = filepath.ToSlash(r)
	if strings.HasSuffix(to, "/") || to == "" {
		r += "/"
	}
	return r
}
