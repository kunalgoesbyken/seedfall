package report

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kunalgoesbyken/seedfall/engine"
)

//go:embed report.html.tmpl
var page string

var tmpl = template.Must(template.New("report").Funcs(template.FuncMap{"percent": Percent}).Parse(page))

func Percent(f float64) string { return strconv.FormatFloat(f*100, 'g', 3, 64) + "%" }

func CheckPath(htmlPath string) error {
	if strings.EqualFold(filepath.Ext(htmlPath), ".json") {
		return fmt.Errorf("report path %s ends in .json, which is where the JSON report goes; use a .html path", htmlPath)
	}
	return nil
}

func Generate(in Input, htmlPath, historyPath string) (Report, error) {
	if err := CheckPath(htmlPath); err != nil {
		return Report{}, err
	}
	entries, err := ReadHistory(historyPath, in.Config)
	if err != nil {
		return Report{}, err
	}
	r, err := Build(in, knownFindings(entries))
	if err != nil {
		return Report{}, err
	}
	r.History = filepath.ToSlash(historyPath)
	r.linkTimelines(filepath.Dir(htmlPath))
	if err := writeHTML(r, htmlPath); err != nil {
		return r, err
	}
	if err := writeJSON(r, jsonPath(htmlPath)); err != nil {
		return r, err
	}
	return r, appendLine(historyPath, r.entry())
}

func (r *Report) linkTimelines(dir string) {
	for _, f := range r.Findings {
		if f.Example == nil || f.Example.Artifacts == "" {
			continue
		}
		page := filepath.Join(filepath.FromSlash(f.Example.Artifacts), engine.ArtifactRun)
		if _, err := os.Stat(page); err != nil {
			continue
		}
		if rel, err := relativePath(dir, page); err == nil {
			f.Example.Timeline = filepath.ToSlash(rel)
		}
	}
}

func relativePath(dir, target string) (string, error) {
	from, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	to, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	return filepath.Rel(from, to)
}

func jsonPath(reportPath string) string {
	return strings.TrimSuffix(reportPath, filepath.Ext(reportPath)) + ".json"
}

func writeHTML(r Report, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, r)
}

func writeJSON(r Report, path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
