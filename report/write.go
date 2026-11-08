package report

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed report.html.tmpl
var page string

var tmpl = template.Must(template.New("report").Funcs(template.FuncMap{"percent": percent}).Parse(page))

func percent(f float64) string { return strconv.FormatFloat(f*100, 'g', 3, 64) + "%" }

func Generate(in Input, htmlPath, historyPath string) (Report, error) {
	entries, err := ReadHistory(historyPath, in.Config)
	if err != nil {
		return Report{}, err
	}
	r := Build(in, knownFindings(entries))
	if err := writeHTML(r, htmlPath); err != nil {
		return r, err
	}
	if err := writeJSON(r, jsonPath(htmlPath)); err != nil {
		return r, err
	}
	return r, appendLine(historyPath, r.entry())
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
