package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/report"
)

const (
	defaultReport = stateDir + "/report.html"
)

var ghCommand = "gh"

func reportHistory(rep report.Report, path string) string {
	return cmp.Or(path, rep.History, filepath.Join(filepath.Dir(rep.Config), stateDir, "history.jsonl"))
}

func cmdNotify(args []string) error {
	fs := newFlags("report notify")
	path := fs.String("report", defaultReport, "triage report written by seedfall run -report")
	history := fs.String("history", "", "run history (default: .seedfall/history.jsonl next to the report's config)")
	webhook := fs.String("webhook", os.Getenv("SEEDFALL_WEBHOOK"), "Slack or Discord incoming webhook URL (default: $SEEDFALL_WEBHOOK)")
	link := fs.String("link", "", "URL to put in the message, for example the CI run")
	dry := fs.Bool("dry-run", false, "print the message instead of posting it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rep, err := report.Load(*path)
	if err != nil {
		return err
	}
	historyPath := reportHistory(rep, *history)
	sent, err := report.ReadEvents(historyPath, rep.Config)
	if err != nil {
		return err
	}
	alerts := rep.Alerts(sent)
	if len(alerts) == 0 {
		fmt.Println("no new or regressed findings to notify")
		return nil
	}
	text := rep.Message(alerts, *link)
	if *dry {
		fmt.Println(text)
		return nil
	}
	if *webhook == "" {
		return errors.New("no webhook: pass -webhook or set SEEDFALL_WEBHOOK")
	}
	if err := report.Post(*webhook, text); err != nil {
		return err
	}
	for _, f := range alerts {
		e := report.Event{Time: time.Now().UTC(), Config: rep.Config, Kind: report.EventNotified, Finding: f.Key, Report: rep.Generated}
		if err := report.AppendEvent(historyPath, e); err != nil {
			return err
		}
	}
	fmt.Printf("notified %d new or regressed findings\n", len(alerts))
	return nil
}

func cmdIssue(args []string) error {
	fs := newFlags("report issue")
	path := fs.String("report", defaultReport, "triage report written by seedfall run -report")
	repo := fs.String("repo", "", "GitHub repository to file issues in, as owner/name")
	dry := fs.Bool("dry-run", false, "print each issue instead of filing it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*dry || *repo != "" {
		if err := checkRepo(*repo); err != nil {
			return err
		}
	}
	rep, err := report.Load(*path)
	if err != nil {
		return err
	}
	if *dry {
		for _, f := range rep.Findings {
			body, err := rep.IssueMarkdown(f)
			if err != nil {
				return err
			}
			fmt.Printf("# %s\n\n%s\n", report.IssueTitle(f), body)
		}
		return nil
	}
	open, err := openIssues(*repo)
	if err != nil {
		return err
	}
	for _, f := range rep.Findings {
		title := report.IssueTitle(f)
		if url, ok := open[title]; ok {
			fmt.Printf("open   %s  %s\n", url, title)
			continue
		}
		body, err := rep.IssueMarkdown(f)
		if err != nil {
			return err
		}
		out, err := gh(body, "issue", "create", "--repo", *repo, "--title", title, "--body-file", "-")
		if err != nil {
			return err
		}
		fmt.Printf("filed  %s  %s\n", lastField(out), title)
	}
	return nil
}

func openIssues(repo string) (map[string]string, error) {
	list := fmt.Sprintf("repos/%s/issues?state=open&per_page=100", repo)
	out, err := gh("", "api", "--paginate", list, "--jq", ".[] | select(.pull_request == null) | {title, url: .html_url}")
	if err != nil {
		return nil, err
	}
	issues, err := decodeStream[struct{ Title, URL string }](out)
	if err != nil {
		return nil, fmt.Errorf("gh api: %w", err)
	}
	byTitle := map[string]string{}
	for _, i := range issues {
		byTitle[i.Title] = i.URL
	}
	return byTitle, nil
}

func gh(stdin string, args ...string) ([]byte, error) {
	cmd := exec.Command(ghCommand, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args[:min(len(args), 2)], " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func decodeStream[T any](b []byte) ([]T, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	var out []T
	for dec.More() {
		var v T
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func lastField(b []byte) string {
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}
