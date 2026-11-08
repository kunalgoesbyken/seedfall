package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/report"
)

const (
	defaultReport  = ".seedfall/report.html"
	issueListLimit = 1000
)

var ghCommand = "gh"

func cmdReportFindings(args []string) error {
	commands := map[string]func([]string) error{"notify": cmdNotify, "issue": cmdIssue}
	if len(args) == 0 || commands[args[0]] == nil {
		return errors.New("usage: seedfall report notify [-report file] [-webhook URL] [-link URL] [-dry-run] | seedfall report issue -repo owner/name [-report file] [-dry-run]")
	}
	return commands[args[0]](args[1:])
}

func reportHistory(rep report.Report, path string) string {
	return cmp.Or(path, filepath.Join(filepath.Dir(rep.Config), ".seedfall", "history.jsonl"))
}

func cmdNotify(args []string) error {
	fs := flag.NewFlagSet("report notify", flag.ExitOnError)
	path := fs.String("report", defaultReport, "triage report written by seedfall run -report")
	history := fs.String("history", "", "run history (default: .seedfall/history.jsonl next to the report's config)")
	webhook := fs.String("webhook", os.Getenv("SEEDFALL_WEBHOOK"), "Slack or Discord incoming webhook URL (default: $SEEDFALL_WEBHOOK)")
	link := fs.String("link", "", "URL to put in the message, for example the CI run")
	dry := fs.Bool("dry-run", false, "print the message instead of posting it")
	fs.Parse(args)
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
	fs := flag.NewFlagSet("report issue", flag.ExitOnError)
	path := fs.String("report", defaultReport, "triage report written by seedfall run -report")
	repo := fs.String("repo", "", "GitHub repository to file issues in, as owner/name")
	dry := fs.Bool("dry-run", false, "print each issue instead of filing it")
	fs.Parse(args)
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
	if *repo == "" {
		return errors.New("-repo owner/name is required")
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
	out, err := gh("", "issue", "list", "--repo", repo, "--state", "open", "--limit", strconv.Itoa(issueListLimit), "--json", "title,url")
	if err != nil {
		return nil, err
	}
	var issues []struct{ Title, URL string }
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
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
		return nil, fmt.Errorf("gh %s %s: %w: %s", args[0], args[1], err, strings.TrimSpace(stderr.String()))
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
