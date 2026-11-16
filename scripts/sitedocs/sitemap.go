package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var landingPages = []string{"", "gym"}

type sitemapURL struct {
	Loc string `xml:"loc"`
}

type sitemap struct {
	XMLName xml.Name     `xml:"urlset"`
	NS      string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

func (s *site) writeSitemap() error {
	base, err := websiteURL(s.root)
	if err != nil {
		return err
	}
	m := sitemap{NS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, rel := range landingPages {
		m.URLs = append(m.URLs, sitemapURL{Loc: base + "/" + rel})
	}
	for _, p := range sortedPages(s.pages) {
		m.URLs = append(m.URLs, sitemapURL{Loc: base + "/" + siteLink(p.url)})
	}
	out, err := xml.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return s.writeFile("sitemap.xml", append([]byte(xml.Header), append(out, '\n')...))
}

func websiteURL(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return "", err
	}
	before, _, found := strings.Cut(string(data), `">Website</a>`)
	if !found {
		return "", fmt.Errorf("no Website link in README.md")
	}
	return strings.TrimSuffix(before[strings.LastIndex(before, `href="`)+len(`href="`):], "/"), nil
}
