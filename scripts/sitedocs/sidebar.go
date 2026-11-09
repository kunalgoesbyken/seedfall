package main

import (
	"path"

	"github.com/yuin/goldmark/ast"
)

const indexSource = "docs/README.md"

type group struct {
	Name  string
	Items []item
}

type item struct {
	Label  string
	Target string
}

func (s *site) sidebar() []group {
	index := s.pages[indexSource]
	if index == nil {
		return nil
	}
	groups := s.indexGroups(index)
	return append(groups, s.unlisted(groups))
}

func (s *site) indexGroups(index *page) []group {
	var groups []group
	for n := index.doc.FirstChild(); n != nil; n = n.NextSibling() {
		switch v := n.(type) {
		case *ast.Heading:
			if v.Level == 2 {
				groups = append(groups, group{Name: plain(v, index.md)})
			}
		case *ast.List:
			if len(groups) > 0 {
				last := &groups[len(groups)-1]
				last.Items = append(last.Items, s.listItems(index, v)...)
			}
		}
	}
	return groups
}

func (s *site) listItems(index *page, list *ast.List) []item {
	var items []item
	ast.Walk(list, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if l, ok := n.(*ast.Link); ok && entering {
			items = append(items, item{plain(l, index.md), s.target(index, string(l.Destination), "blob")})
		}
		return ast.WalkContinue, nil
	})
	return items
}

func (s *site) unlisted(groups []group) group {
	listed := map[string]bool{}
	for _, g := range groups {
		for _, it := range g.Items {
			listed[it.Target] = true
		}
	}
	more := group{Name: "More"}
	for _, p := range sortedPages(s.pages) {
		target := "/" + siteLink(p.url)
		if path.Dir(p.src) == "docs" && p.src != indexSource && !listed[target] {
			more.Items = append(more.Items, item{p.title, target})
		}
	}
	return more
}
