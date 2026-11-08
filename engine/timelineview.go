package engine

import (
	"bufio"
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strings"
	"time"
)

//go:embed timeline.html.tmpl
var timelinePage string

var timelineTmpl = template.Must(template.New("timeline").Parse(timelinePage))

func (t *Timeline) WriteHTML(w io.Writer, title string) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return timelineTmpl.Execute(w, struct {
		Title string
		Data  template.JS
	}{title, template.JS(data)})
}

type mermaidLine struct {
	at   time.Duration
	text string
}

func (t *Timeline) WriteMermaid(w io.Writer) error {
	alias := map[string]string{}
	b := bufio.NewWriter(w)
	fmt.Fprintln(b, "sequenceDiagram")
	for i, n := range t.Nodes {
		alias[n] = fmt.Sprintf("p%d", i)
		fmt.Fprintf(b, "  participant %s as %s\n", alias[n], mermaidText(n))
	}
	over := func(nodes []string) string {
		known := slices.DeleteFunc(slices.Clone(nodes), func(n string) bool { return alias[n] == "" })
		if len(known) == 0 && len(t.Nodes) > 0 {
			known = []string{t.Nodes[0], t.Nodes[len(t.Nodes)-1]}
		}
		if len(known) == 0 {
			return ""
		}
		parts := make([]string, len(known))
		for i, n := range known {
			parts[i] = alias[n]
		}
		return "Note over " + strings.Join(parts, ",") + ": "
	}
	var lines []mermaidLine
	note := func(at time.Duration, nodes []string, text string) {
		if prefix := over(nodes); prefix != "" {
			lines = append(lines, mermaidLine{at, prefix + mermaidText(fmtTime(int64(at))+" "+text)})
		}
	}
	for _, l := range t.Lives {
		note(l.Start, []string{l.Node}, "start "+l.Instance)
		if l.End >= 0 {
			note(l.End, []string{l.Node}, l.Instance+" "+l.Ended)
		}
	}
	for _, f := range t.Faults {
		note(f.Start, f.Nodes, f.Text)
	}
	for _, m := range t.Marks {
		text := m.Text
		if m.Kind == MarkFailure {
			text = "FAIL " + text
		}
		note(m.At, []string{m.Node}, text)
	}
	for _, m := range t.Messages {
		if alias[m.From] == "" || alias[m.To] == "" {
			continue
		}
		lines = append(lines, mermaidLine{m.Sent, fmt.Sprintf("%s%s%s: %s", alias[m.From], m.arrow(), alias[m.To], mermaidText(m.label()))})
	}
	slices.SortStableFunc(lines, func(a, b mermaidLine) int { return cmp.Compare(a.at, b.at) })
	for _, l := range lines {
		fmt.Fprintf(b, "  %s\n", l.text)
	}
	return b.Flush()
}

func (m Message) arrow() string {
	switch {
	case !m.Delivered():
		return "-x"
	case m.Kind == MessageEOF || m.Kind == MessageReset:
		return "-->>"
	}
	return "->>"
}

func (m Message) label() string {
	what := m.Kind
	if m.Kind == MessageData {
		what = fmt.Sprintf("%d bytes", m.Bytes)
	}
	fate := "never read"
	if m.Delivered() {
		fate = "read " + fmtTime(int64(m.Read))
	}
	text := fmt.Sprintf("%s conn %d %s, %s", fmtTime(int64(m.Sent)), m.Conn, what, fate)
	if m.Held {
		text += ", held"
	}
	if m.Duplicate {
		text += ", duplicate"
	}
	return text
}

var mermaidEscapes = strings.NewReplacer("#", "#35;", ";", "#59;", "\n", " ", "\r", " ")

func mermaidText(s string) string { return mermaidEscapes.Replace(s) }
