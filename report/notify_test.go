package report

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

func generated(t *testing.T, dir string, in Input) Report {
	t.Helper()
	r, err := Generate(in, filepath.Join(dir, "report.html"), filepath.Join(dir, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAlertsOnlyNewAndRegressedAndOncePerReport(t *testing.T) {
	dir := t.TempDir()
	history := filepath.Join(dir, "history.jsonl")
	first := generated(t, dir, input("1-100", 99, failing(1)))
	if got := first.Alerts(nil); len(got) != 1 || got[0].Status != StatusNew {
		t.Fatalf("a new finding must alert: %+v", got)
	}
	sent := Event{Time: time.Now(), Config: first.Config, Kind: EventNotified, Finding: lost.Key(), Report: first.Generated}
	if err := AppendEvent(history, sent); err != nil {
		t.Fatal(err)
	}
	events, err := ReadEvents(history, first.Config)
	if err != nil || len(events) != 1 {
		t.Fatalf("events %+v, %v", events, err)
	}
	if got := first.Alerts(events); len(got) != 0 {
		t.Fatalf("the same report must not alert twice: %+v", got)
	}
	runs, err := ReadHistory(history, first.Config)
	if err != nil || len(runs) != 1 {
		t.Fatalf("event lines must not count as runs: %+v, %v", runs, err)
	}
	second := generated(t, dir, input("101-200", 99, failing(150)))
	if got := second.Alerts(events); len(got) != 0 {
		t.Fatalf("an ongoing finding must not alert: %+v", got)
	}
}

func TestMessageStaysUnderTheLimit(t *testing.T) {
	var failures []engine.Result
	for i := range 200 {
		res := failing(uint64(i + 1))
		res.Failure = engine.Failure{Class: engine.FailProperty, Node: "client", Property: fmt.Sprintf("property %d %s", i, strings.Repeat("x", 40))}
		failures = append(failures, res)
	}
	r := Build(input("1-400", 200, failures...), map[string]known{})
	msg := r.Message(r.Alerts(nil), "https://ci.example/run/1")
	if len(msg) > messageLimit || !strings.Contains(msg, "more") || !strings.Contains(msg, "seedfall replay -c seedfall.json -seed") || !strings.Contains(msg, "https://ci.example/run/1") {
		t.Fatalf("message of %d bytes:\n%s", len(msg), msg)
	}
}

func TestPostSendsSlackAndDiscordFieldsAndHidesTheURL(t *testing.T) {
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken/secret" {
			http.Error(w, "no such hook", http.StatusNotFound)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer server.Close()
	if err := Post(server.URL+"/hook", "hello"); err != nil {
		t.Fatal(err)
	}
	if got["text"] != "hello" || got["content"] != "hello" {
		t.Fatalf("payload %v", got)
	}
	err := Post(server.URL+"/broken/secret", "hello")
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "no such hook") {
		t.Fatalf("a refused post must fail with the answer: %v", err)
	}
	err = Post("http://127.0.0.1:1/secret-token", "hello")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("a failed post must not leak the webhook URL: %v", err)
	}
}

func TestIssueMarkdownAndHTMLCopyButton(t *testing.T) {
	dir := t.TempDir()
	in := input("1-100", 98, failing(3), failing(9))
	in.Version, in.Commit = "v9.9.9", "abc123"
	in.Regressions = []Regression{{Source: "seedfall-regressions/seedfall-seed-1.json", Seed: 1, Passed: true}, {Source: "seeds.txt:2", Seed: 7, Failure: &lost, Reason: "client#1: acknowledged write lost", Artifacts: ".seedfall/failures/seed-7"}}
	r := generated(t, dir, in)
	md, err := r.IssueMarkdown(r.Findings[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"**assert · client**", "seedfall replay -c seedfall.json -seed 3", "Example seeds: 3, 9", "crash server", "| seedfall | v9.9.9 |", "| commit | abc123 |", ".seedfall/failures/seed-3"} {
		if !strings.Contains(md, want) {
			t.Errorf("issue markdown lacks %q:\n%s", want, md)
		}
	}
	if IssueTitle(r.Findings[0]) != "seedfall finding: assert · client" {
		t.Errorf("title %q", IssueTitle(r.Findings[0]))
	}
	page, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Copy issue markdown", "seedfall replay -c seedfall.json -seed 3", "<h2>Regressions</h2>", "seeds.txt:2", "seedfall v9.9.9", "commit abc123"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("HTML report lacks %q", want)
		}
	}
	if r.RegressionsFailed() != 1 {
		t.Errorf("RegressionsFailed = %d, want 1", r.RegressionsFailed())
	}
}
