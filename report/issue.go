package report

import (
	_ "embed"
	"strings"
	"text/template"
)

//go:embed issue.md.tmpl
var issuePage string

var issueTmpl = template.Must(template.New("issue").Funcs(template.FuncMap{"percent": Percent}).Parse(issuePage))

const issueLabel = "seedfall finding"

func IssueTitle(f Finding) string { return issueLabel + ": " + f.Key }

func (r Report) IssueMarkdown(f Finding) (string, error) {
	var b strings.Builder
	err := issueTmpl.Execute(&b, struct {
		Report  Report
		Finding Finding
	}{r, f})
	return b.String(), err
}
