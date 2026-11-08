package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kunalgoesbyken/seedfall/engine"
	"github.com/kunalgoesbyken/seedfall/report"
)

func TestReportGitHubAppendsOutputs(t *testing.T) {
	path, _ := writeTestReport(t)
	output := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(output, []byte("status=failed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdGitHub([]string{"-report", path, "-output", output}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), "status=failed\nfailing-seed=3\nfindings=2\n"; got != want {
		t.Errorf("outputs %q, want %q", got, want)
	}
}

func useFakeGH(t *testing.T, existing string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "gh.log")
	t.Setenv(fakeGHEnv, log)
	t.Setenv(fakeGHCommentEnv, existing)
	ghCommand = os.Args[0]
	t.Cleanup(func() { ghCommand = "gh" })
	return log
}

func TestReportCommentCreatesOneCommentThenUpdatesIt(t *testing.T) {
	path, _ := writeTestReport(t)
	args := []string{"-report", path, "-repo", "o/r", "-pr", "12", "-marker", "job-a", "-link", "https://example.test/run"}

	log := useFakeGH(t, "")
	if err := cmdComment(args); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	created := string(b)
	if !strings.Contains(created, "api --method POST repos/o/r/issues/12/comments --input -") || !strings.Contains(created, "seedfall-comment:job-a") || !strings.Contains(created, "seedfall replay -c ") {
		t.Fatalf("created comment:\n%s", created)
	}

	log = useFakeGH(t, report.CommentMarker("job-a")+"\nolder result")
	if err := cmdComment(args); err != nil {
		t.Fatal(err)
	}
	if b, err = os.ReadFile(log); err != nil {
		t.Fatal(err)
	}
	if updated := string(b); !strings.Contains(updated, "api --method PATCH repos/o/r/issues/comments/"+strconv.Itoa(fakeCommentID)+" --input -") || strings.Contains(updated, "--method POST") {
		t.Fatalf("updated comment:\n%s", updated)
	}
}

func TestReportCommentLeavesOtherJobsCommentsAlone(t *testing.T) {
	path, _ := writeTestReport(t)
	log := useFakeGH(t, report.CommentMarker("job-b")+"\nother job")
	if err := cmdComment([]string{"-report", path, "-repo", "o/r", "-pr", "12", "-marker", "job-a"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "--method POST") || strings.Contains(string(b), "--method PATCH") {
		t.Fatalf("calls:\n%s", b)
	}
}

func TestReportCommentPostsABodyFile(t *testing.T) {
	proof := filepath.Join(t.TempDir(), "proof.md")
	if err := os.WriteFile(proof, []byte("### seedfall verify: PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := useFakeGH(t, "")
	if err := cmdComment([]string{"-body", proof, "-repo", "o/r", "-pr", "12"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "seedfall verify: PASS") {
		t.Fatalf("calls:\n%s", b)
	}
}

func TestReportCommentNeedsATarget(t *testing.T) {
	path, _ := writeTestReport(t)
	if err := cmdComment([]string{"-report", path, "-repo", "o/r"}); err == nil {
		t.Error("no error without -pr")
	}
}

func TestMutateMarkdownNamesEachMutantAndEscapesTables(t *testing.T) {
	seed := uint64(2)
	a := mutateArgs{Seeds: "1-5"}
	a.Config = "seedfall.json"
	v := mutateView{Survived: 1, Mutants: []mutantView{
		{Mutant: "fsync_noop", Outcome: string(engine.MutantKilled), KilledBy: &seed, Reason: "lost | gone"},
		{Mutant: "net_dup_chunk", Outcome: string(engine.MutantSurvived), SeedsPassed: 5, Weakened: 2},
	}}
	got := v.markdown(a)
	for _, want := range []string{"### seedfall mutate: FAIL", "`seedfall.json`", "| fsync_noop | killed seed 2: lost \\| gone |", "| net_dup_chunk | SURVIVED 5 seeds passed"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if clean := (mutateView{}).markdown(a); !strings.Contains(clean, "### seedfall mutate: PASS") {
		t.Errorf("clean:\n%s", clean)
	}
}
