package main

import (
	"html"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

var kindFigure = ast.NewNodeKind("Figure")

type figure struct {
	ast.BaseBlock
	image *ast.Image
}

func (f *figure) Kind() ast.NodeKind { return kindFigure }

func (f *figure) Dump(src []byte, level int) { ast.DumpHelper(f, src, level, nil, nil) }

func makeFigures(doc ast.Node) {
	for n := doc.FirstChild(); n != nil; {
		next := n.NextSibling()
		if img := lone(n); img != nil {
			doc.ReplaceChild(doc, n, &figure{image: img})
		}
		n = next
	}
}

func lone(n ast.Node) *ast.Image {
	if n.Kind() != ast.KindParagraph || n.ChildCount() != 1 {
		return nil
	}
	img, _ := n.FirstChild().(*ast.Image)
	return img
}

type figureRenderer struct{}

func (figureRenderer) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(kindFigure, renderFigure)
}

func renderFigure(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	f := n.(*figure)
	w.WriteString(`<figure><img src="` + html.EscapeString(string(f.image.Destination)) + `" alt="` + html.EscapeString(plain(f.image, src)) + `" loading="lazy">`)
	if len(f.image.Title) > 0 {
		w.WriteString("<figcaption>" + html.EscapeString(string(f.image.Title)) + "</figcaption>")
	}
	w.WriteString("</figure>\n")
	return ast.WalkSkipChildren, nil
}
