package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

type page struct {
	src   string
	url   string
	md    []byte
	doc   ast.Node
	title string
	desc  string
	ids   map[string]bool
	body  string
}

type site struct {
	root     string
	repo     string
	pages    map[string]*page
	files    map[string]bool
	problems map[string]bool
	groups   []group
	md       goldmark.Markdown
}

func run(root string) ([]string, error) {
	s, err := load(root)
	if err != nil {
		return nil, err
	}
	if err := s.write(); err != nil {
		return nil, err
	}
	return s.report(), nil
}

func load(root string) (*site, error) {
	repo, err := repoURL(root)
	if err != nil {
		return nil, err
	}
	s := &site{
		root:     root,
		repo:     repo,
		pages:    map[string]*page{},
		files:    map[string]bool{},
		problems: map[string]bool{},
		md: goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			goldmark.WithRendererOptions(html.WithUnsafe()),
		),
	}
	if err := s.scan(); err != nil {
		return nil, err
	}
	s.groups = s.sidebar()
	return s, nil
}

func repoURL(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if mod, ok := strings.CutPrefix(line, "module "); ok {
			return "https://" + strings.TrimSpace(mod), nil
		}
	}
	return "", fmt.Errorf("no module line in go.mod")
}

func (s *site) scan() error {
	return filepath.WalkDir(filepath.Join(s.root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		return s.add(filepath.ToSlash(rel))
	})
}

func (s *site) add(src string) error {
	if path.Ext(src) != ".md" {
		s.files[src] = true
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(src)))
	if err != nil {
		return err
	}
	p := &page{src: src, url: pageURL(src), md: bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))}
	p.doc = s.md.Parser().Parse(text.NewReader(p.md))
	p.ids = headingIDs(p.doc)
	if err := describe(p); err != nil {
		return err
	}
	s.pages[src] = p
	return nil
}

func pageURL(src string) string {
	base := strings.TrimSuffix(src, ".md")
	if path.Base(base) == "README" {
		base = path.Join(path.Dir(base), "index")
	}
	return base + ".html"
}

func markdownURL(p *page) string {
	return strings.TrimSuffix(p.url, ".html") + ".md"
}

func (s *site) write() error {
	if err := os.RemoveAll(filepath.Join(s.root, "site", "docs")); err != nil {
		return err
	}
	for _, p := range sortedPages(s.pages) {
		if err := s.render(p); err != nil {
			return err
		}
		if err := s.writePage(p); err != nil {
			return err
		}
	}
	return s.copyFiles()
}

func (s *site) writePage(p *page) error {
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, s.view(p)); err != nil {
		return err
	}
	if err := s.writeFile(p.url, buf.Bytes()); err != nil {
		return err
	}
	return s.writeFile(markdownURL(p), p.md)
}

func (s *site) copyFiles() error {
	for src := range s.files {
		data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(src)))
		if err != nil {
			return err
		}
		if err := s.writeFile(src, data); err != nil {
			return err
		}
	}
	return nil
}

func (s *site) writeFile(rel string, data []byte) error {
	name := filepath.Join(s.root, "site", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

func (s *site) report() []string {
	var out []string
	for p := range s.problems {
		out = append(out, p)
	}
	return out
}
