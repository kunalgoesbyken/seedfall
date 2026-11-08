package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
)

const fakeGHEnv = "SEEDFALL_FAKE_GH"

func TestMain(m *testing.M) {
	if log := os.Getenv(fakeGHEnv); log != "" {
		os.Exit(fakeGH(log, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeGH(log string, args []string) int {
	switch strings.Join(args[:2], " ") {
	case "issue list":
		fmt.Print(`[{"title": "seedfall finding: crash · server", "url": "https://github.com/o/r/issues/1"}]`)
	case "issue create":
		body, _ := io.ReadAll(os.Stdin)
		f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return 1
		}
		defer f.Close()
		fmt.Fprintf(f, "%s\n%s\n=====\n", strings.Join(args, " "), body)
		fmt.Println("https://github.com/o/r/issues/2")
	default:
		return 1
	}
	return 0
}

func writeTestReport(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(dir, "seedfall.json")
	lost := engine.Result{Seed: 3, Failed: true, Failure: engine.Failure{Class: engine.FailAssert, Node: "client"}, Reason: "client#1: acknowledged write lost", At: time.Second}
	crash := engine.Result{Seed: 5, Failed: true, Failure: engine.Failure{Class: engine.FailCrash, Node: "server"}, Reason: "server#1 crashed"}
	byseed := map[uint64]engine.Result{3: lost, 5: crash}
	in := report.Input{
		Config: config, Seeds: "1-20", Version: "v1.2.3", Summary: engine.Summary{Passed: 18, Failures: []engine.Result{lost, crash}},
		Replay: func(seed uint64) report.Replayed {
			return report.Replayed{Result: byseed[seed], Class: engine.Classification{Verdict: engine.VerdictBug}}
		},
	}
	path := filepath.Join(dir, "report.html")
	if _, err := report.Generate(in, path, filepath.Join(dir, ".seedfall", "history.jsonl")); err != nil {
		t.Fatal(err)
	}
	return path, filepath.Join(dir, ".seedfall", "history.jsonl")
}

func TestNotifyPostsNewFindingsOnce(t *testing.T) {
	path, history := writeTestReport(t)
	var posts atomic.Int32
	var body atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body.Store(string(b))
		posts.Add(1)
	}))
	defer server.Close()
	for range 2 {
		if err := cmdNotify([]string{"-report", path, "-webhook", server.URL, "-link", "https://ci.example/1"}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := body.Load().(string)
	if posts.Load() != 1 || !strings.Contains(got, "assert · client") || !strings.Contains(got, "crash · server") || !strings.Contains(got, "https://ci.example/1") {
		t.Fatalf("%d posts, last %s", posts.Load(), got)
	}
	rep, err := report.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	events, err := report.ReadEvents(history, rep.Config)
	if err != nil || len(events) != 2 {
		t.Fatalf("events %+v, %v", events, err)
	}
}

func TestIssueFilesOnlyFindingsWithoutAnOpenIssue(t *testing.T) {
	path, _ := writeTestReport(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	t.Setenv(fakeGHEnv, log)
	ghCommand = os.Args[0]
	t.Cleanup(func() { ghCommand = "gh" })
	if err := cmdIssue([]string{"-report", path, "-repo", "o/r"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	created := string(b)
	if strings.Count(created, "=====") != 1 || !strings.Contains(created, "--title seedfall finding: assert · client") || strings.Contains(created, "crash · server") {
		t.Fatalf("issues created:\n%s", created)
	}
	if !strings.Contains(created, "seedfall replay -c ") || !strings.Contains(created, "| seedfall | v1.2.3 |") {
		t.Fatalf("issue body lacks the replay command or version:\n%s", created)
	}
}

func TestReportHistoryIgnoresOverridesInTheConfigReference(t *testing.T) {
	ref := engine.ConfigRef{Path: "examples/upgrade/seedfall.json", Sets: []string{"nodes.ledger.disk=state/ledger"}}
	saved := report.Report{Config: ref.String(), History: "examples/upgrade/.seedfall/history.jsonl"}
	if got := reportHistory(saved, ""); got != saved.History {
		t.Errorf("history = %q, want the one the report recorded, %q", got, saved.History)
	}
	if got := reportHistory(saved, "other.jsonl"); got != "other.jsonl" {
		t.Errorf("history = %q, want the explicit path", got)
	}
}
