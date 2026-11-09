package report

import (
	"cmp"
	_ "embed"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

const (
	commentLimit  = 65000
	commentCut    = "\n\n… cut to fit a GitHub comment, see the report artifact for the rest\n"
	annotateTitle = "seedfall"
	markerFormat  = "<!-- seedfall-comment:%s -->"
)

//go:embed comment.md.tmpl
var commentPage string

var commentTmpl = template.Must(template.New("comment").Funcs(template.FuncMap{"percent": Percent, "code": code}).Parse(commentPage))

var annotateData = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")

var annotateProperty = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")

var attentionOrder = []Status{StatusRegressed, StatusNew, StatusOngoing}

func code(s string) string {
	return "`" + strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "`", "'") + "`"
}

type Annotation struct {
	File    string
	Line    int
	Title   string
	Message string
}

func (a Annotation) String() string {
	var props []string
	if a.File != "" {
		props = append(props, "file="+annotateProperty.Replace(a.File), "line="+strconv.Itoa(a.Line))
	}
	props = append(props, "title="+annotateProperty.Replace(a.Title))
	return "::error " + strings.Join(props, ",") + "::" + annotateData.Replace(a.Message)
}

func (r Report) configDir() string {
	if r.History != "" {
		return filepath.Dir(filepath.Dir(r.History))
	}
	return filepath.Dir(r.Config)
}

func (r Report) source(location, workdir string) (string, int) {
	at := strings.LastIndexByte(location, ':')
	if at < 0 {
		return "", 0
	}
	line, err := strconv.Atoi(location[at+1:])
	if err != nil {
		return "", 0
	}
	path := filepath.Join(r.configDir(), filepath.FromSlash(location[:at]))
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}
	rel, err := filepath.Rel(workdir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", 0
	}
	return filepath.ToSlash(rel), line
}

func (r Report) attentionFindings() []Finding {
	out := slices.Clone(r.Findings)
	rank := func(f Finding) int {
		if i := slices.Index(attentionOrder, f.Status); i >= 0 {
			return i
		}
		return len(attentionOrder)
	}
	slices.SortStableFunc(out, func(a, b Finding) int { return cmp.Compare(rank(a), rank(b)) })
	return out
}

func (r Report) Annotations(workdir string) []Annotation {
	locations := map[string]string{}
	for _, p := range r.Properties {
		locations[p.Message] = p.Location
	}
	var out []Annotation
	for _, reg := range r.FailedRegressions() {
		out = append(out, Annotation{Title: annotateTitle + ": pinned regression", Message: "seed " + strconv.FormatUint(reg.Seed, 10) + " from " + reg.Source + " fails again: " + reg.Reason})
	}
	for _, f := range r.attentionFindings() {
		a := Annotation{Title: annotateTitle + ": " + f.Key, Message: string(f.Status) + " finding, " + strconv.Itoa(f.Count) + " seeds failed"}
		if f.Example != nil {
			a.Message += ", e.g. seed " + strconv.FormatUint(f.Example.Seed, 10) + ": " + f.Example.Reason + "\nreplay: " + f.Example.Replay
		}
		if f.Failure.Property != "" {
			a.File, a.Line = r.source(locations[f.Failure.Property], workdir)
		}
		out = append(out, a)
	}
	for _, p := range r.UnmetProperties() {
		a := Annotation{Title: annotateTitle + ": property " + p.Problem, Message: p.Kind + " " + p.Message + ": " + p.Problem}
		a.File, a.Line = r.source(p.Location, workdir)
		out = append(out, a)
	}
	return out
}

func (r Report) FailingSeed() (uint64, bool) {
	if failed := r.FailedRegressions(); len(failed) > 0 {
		return failed[0].Seed, true
	}
	for _, f := range r.attentionFindings() {
		if len(f.Seeds) > 0 {
			return f.Seeds[0], true
		}
	}
	return 0, false
}

type commentView struct {
	Report      Report
	Link        string
	Passed      bool
	Regressions []Regression
	Regressed   []Finding
	New         []Finding
	Ongoing     []Finding
	Resolved    []Finding
	Unmet       []Property
}

func (r Report) CommentMarkdown(link string) (string, error) {
	v := commentView{Report: r, Link: link, Regressions: r.FailedRegressions(), Unmet: r.UnmetProperties()}
	for _, f := range r.Findings {
		switch f.Status {
		case StatusRegressed:
			v.Regressed = append(v.Regressed, f)
		case StatusNew:
			v.New = append(v.New, f)
		case StatusOngoing:
			v.Ongoing = append(v.Ongoing, f)
		}
	}
	for _, f := range r.Cleared {
		if f.Status == StatusResolved {
			v.Resolved = append(v.Resolved, f)
		}
	}
	v.Passed = r.Failed == 0 && len(v.Regressions) == 0 && len(v.Unmet) == 0
	var b strings.Builder
	err := commentTmpl.Execute(&b, v)
	return b.String(), err
}

func CommentMarker(marker string) string { return strings.Replace(markerFormat, "%s", marker, 1) }

func CommentBody(marker, text string) string {
	body := CommentMarker(marker) + "\n" + text
	if len(body) <= commentLimit {
		return body
	}
	cut := strings.ToValidUTF8(body[:commentLimit-len(commentCut)], "")
	if at := strings.LastIndexByte(cut, '\n'); at > 0 {
		cut = cut[:at]
	}
	return cut + commentCut
}
