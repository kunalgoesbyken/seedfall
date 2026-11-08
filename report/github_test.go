package report

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kunalgoesbyken/seedfall/engine"
)

func gitHubReport(workdir string) Report {
	return Report{
		Config: "svc/seedfall.json", History: filepath.Join(workdir, "svc", ".seedfall", "history.jsonl"), Seeds: "1-20", Total: 20, Failed: 3, Version: "v1",
		Regressions: []Regression{{Source: "seedfall-regressions/a.json", Seed: 9, Reason: "torn"}, {Source: "seedfall-regressions/b.json", Seed: 4, Passed: true}},
		Findings: []Finding{
			{Key: "assert · client", Status: StatusOngoing, Count: 2, Rate: 0.1, Seeds: []uint64{7}, Example: &Example{Seed: 7, Reason: "lost", Replay: "seedfall replay -c svc/seedfall.json -seed 7"}},
			{Key: "property · client · total", Status: StatusNew, Count: 1, Rate: 0.05, Failure: engine.Failure{Class: engine.FailProperty, Node: "client", Property: "total"}, Seeds: []uint64{3}, Example: &Example{Seed: 3, Reason: "100%\ndone", Replay: "seedfall replay -c svc/seedfall.json -seed 3"}},
			{Key: "crash · server", Status: StatusRegressed, Count: 1, Rate: 0.05, Seeds: []uint64{5}, Example: &Example{Seed: 5, Reason: "boom", Replay: "seedfall replay -c svc/seedfall.json -seed 5"}},
		},
		Cleared: []Finding{
			{Key: "exit · server", Status: StatusResolved, Note: "not seen in 20 seeds"},
			{Key: "exit · client", Status: StatusUnconfirmed, Note: "needs more seeds"},
		},
		Properties: []Property{
			{Kind: "always", Message: "total", Location: "client/main.go:12"},
			{Kind: "sometimes", Message: "rare path", Location: "client/main.go:40", Problem: "never reached"},
			{Kind: "sometimes", Message: "dependency", Location: "../../outside/dep.go:3", Problem: "never true"},
		},
	}
}

func TestAnnotationsPutFailedRegressionsThenUrgentFindingsFirst(t *testing.T) {
	workdir := t.TempDir()
	got := gitHubReport(workdir).Annotations(workdir)
	want := []string{"pinned regression", "crash · server", "property · client · total", "assert · client", "property never reached", "property never true"}
	if len(got) != len(want) {
		t.Fatalf("%d annotations: %+v", len(got), got)
	}
	for i, title := range want {
		if got[i].Title != "seedfall: "+title {
			t.Errorf("annotation %d titled %q, want %q", i, got[i].Title, title)
		}
	}
}

func TestAnnotationsLocateCatalogedPropertiesInsideTheWorkspaceOnly(t *testing.T) {
	workdir := t.TempDir()
	got := gitHubReport(workdir).Annotations(workdir)
	if got[2].File != "svc/client/main.go" || got[2].Line != 12 {
		t.Errorf("property finding at %s:%d", got[2].File, got[2].Line)
	}
	if got[3].File != "" {
		t.Errorf("a finding without a property has file %q", got[3].File)
	}
	if got[4].File != "svc/client/main.go" || got[4].Line != 40 {
		t.Errorf("unmet property at %s:%d", got[4].File, got[4].Line)
	}
	if got[5].File != "" {
		t.Errorf("a location outside the workspace was kept as %q", got[5].File)
	}
}

func TestAnnotationEscapesWorkflowCommandSyntax(t *testing.T) {
	a := Annotation{File: "a,b.go", Line: 3, Title: "seedfall: x", Message: "100%\nreplay::y"}
	want := "::error file=a%2Cb.go,line=3,title=seedfall%3A x::100%25%0Areplay::y"
	if got := a.String(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := (Annotation{Title: "t", Message: "m"}).String(); got != "::error title=t::m" {
		t.Errorf("without a file: %s", got)
	}
}

func TestFailingSeedPrefersRegressionsThenUrgency(t *testing.T) {
	r := gitHubReport(t.TempDir())
	if s, ok := r.FailingSeed(); !ok || s != 9 {
		t.Errorf("with a failed regression: %d %v", s, ok)
	}
	r.Regressions = nil
	if s, ok := r.FailingSeed(); !ok || s != 5 {
		t.Errorf("with a regressed finding: %d %v", s, ok)
	}
	r.Findings = nil
	if s, ok := r.FailingSeed(); ok {
		t.Errorf("clean report gave seed %d", s)
	}
}

func TestCommentListsWhatChangedWithReplayCommands(t *testing.T) {
	body, err := gitHubReport(t.TempDir()).CommentMarkdown("https://example.test/run")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"### seedfall: FAIL", "3 of 20 seeds failed", "https://example.test/run",
		"**Pinned regressions failing again**", "seed 9 from `seedfall-regressions/a.json`",
		"**Regressed**", "`seedfall replay -c svc/seedfall.json -seed 5`",
		"**New**", "`seedfall replay -c svc/seedfall.json -seed 3`",
		"**Resolved**", "`exit · server`: not seen in 20 seeds",
		"**Ongoing**", "`seedfall replay -c svc/seedfall.json -seed 7`",
		"sometimes `rare path` at `client/main.go:40`: never reached",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("comment lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "exit · client") {
		t.Errorf("a finding that is only not seen is listed:\n%s", body)
	}
	if strings.Index(body, "**Regressed**") > strings.Index(body, "**New**") {
		t.Errorf("regressed findings should come first:\n%s", body)
	}
}

func TestCommentOnACleanRunPasses(t *testing.T) {
	body, err := Report{Config: "s.json", Seeds: "1-5", Total: 5, Version: "v1"}.CommentMarkdown("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "### seedfall: PASS") || strings.Contains(body, "**") {
		t.Errorf("clean comment:\n%s", body)
	}
}

func TestCommentKeepsUntrustedTextInsideOneCodeSpan(t *testing.T) {
	got := code("ping @org/team\n`run`  now")
	if want := "`ping @org/team 'run' now`"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCommentBodyStartsWithItsMarkerAndFitsGitHub(t *testing.T) {
	if body := CommentBody("job", "short"); body != CommentMarker("job")+"\nshort" {
		t.Errorf("short body %q", body)
	}
	long := strings.Repeat("é line of text\n", commentLimit)
	body := CommentBody("job", long)
	if !strings.HasPrefix(body, CommentMarker("job")) || len(body) > commentLimit || !utf8.ValidString(body) || !strings.HasSuffix(body, commentCut) {
		t.Errorf("long body: %d bytes, valid %v, ends %q", len(body), utf8.ValidString(body), body[len(body)-40:])
	}
}
