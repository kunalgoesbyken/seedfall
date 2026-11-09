package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
)

const descLimit = 200

func describe(p *page) error {
	h := firstHeading(p.doc)
	if h == nil {
		return fmt.Errorf("%s has no level 1 heading", p.src)
	}
	p.title = plain(h, p.md)
	if para := firstParagraph(p.doc); para != nil {
		p.desc = clip(plain(para, p.md), descLimit)
	}
	return nil
}

func firstHeading(doc ast.Node) *ast.Heading {
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok && h.Level == 1 {
			return h
		}
	}
	return nil
}

func firstParagraph(doc ast.Node) ast.Node {
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if n.Kind() == ast.KindParagraph {
			return n
		}
	}
	return nil
}

func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	cut := string(r[:limit])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, ",;:") + "…"
}

func headingIDs(doc ast.Node) map[string]bool {
	ids := map[string]bool{}
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		if id, ok := h.AttributeString("id"); ok {
			ids[string(id.([]byte))] = true
		}
		return ast.WalkContinue, nil
	})
	return ids
}

func sortedPages(pages map[string]*page) []*page {
	out := make([]*page, 0, len(pages))
	for _, p := range pages {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].src < out[j].src })
	return out
}

func (s *site) render(p *page) error {
	s.rewriteLinks(p)
	var buf bytes.Buffer
	if err := s.md.Renderer().Render(&buf, p.md, p.doc); err != nil {
		return err
	}
	p.body = wrapTables(buf.String())
	return nil
}

func wrapTables(h string) string {
	return strings.NewReplacer(
		"<table>", `<div class="table-scroll"><table>`,
		"</table>", "</table></div>",
	).Replace(h)
}
