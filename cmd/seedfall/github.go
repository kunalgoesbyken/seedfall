package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kunalgoesbyken/seedfall/report"
)

const defaultCommentMarker = "seedfall"

func cmdGitHub(args []string) error {
	fs := newFlags("report github")
	path := fs.String("report", defaultReport, "triage report written by seedfall run -report")
	output := fs.String("output", os.Getenv("GITHUB_OUTPUT"), "file to append failing-seed and findings to (default: $GITHUB_OUTPUT, or print them)")
	md := fs.String("md", "", "also write the pull request comment's Markdown to this file, for a job summary or report comment -body")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rep, err := report.Load(*path)
	if err != nil {
		return err
	}
	if *md != "" {
		text, err := rep.CommentMarkdown("")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*md, []byte(text), 0o644); err != nil {
			return err
		}
	}
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}
	for _, a := range rep.Annotations(workdir) {
		fmt.Println(a)
	}
	seed := ""
	if s, ok := rep.FailingSeed(); ok {
		seed = strconv.FormatUint(s, 10)
	}
	lines := fmt.Sprintf("failing-seed=%s\nfindings=%d\n", seed, len(rep.Findings))
	if *output == "" {
		fmt.Print(lines)
		return nil
	}
	f, err := os.OpenFile(*output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(lines)
	return err
}

func cmdComment(args []string) error {
	fs := newFlags("report comment")
	path := fs.String("report", defaultReport, "triage report written by seedfall run -report")
	bodyFile := fs.String("body", "", "post this Markdown file instead of the report, for example a verify proof")
	repo := fs.String("repo", "", "GitHub repository, as owner/name")
	pr := fs.Int("pr", 0, "pull request number")
	marker := fs.String("marker", defaultCommentMarker, "name of the comment to update; use one per job in a matrix")
	link := fs.String("link", "", "URL to put in the comment, for example the CI run")
	dry := fs.Bool("dry-run", false, "print the comment instead of posting it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	text, err := commentText(*path, *bodyFile, *link)
	if err != nil {
		return err
	}
	body := report.CommentBody(*marker, text)
	if *dry {
		fmt.Print(body)
		return nil
	}
	if *repo == "" || *pr <= 0 {
		return errors.New("-repo owner/name and -pr N are required")
	}
	return upsertComment(*repo, *pr, *marker, body)
}

func commentText(reportPath, bodyFile, link string) (string, error) {
	if bodyFile != "" {
		b, err := os.ReadFile(bodyFile)
		if err != nil || link == "" {
			return string(b), err
		}
		return fmt.Sprintf("%s\n\n[Workflow run](%s)\n", strings.TrimRight(string(b), "\n"), link), nil
	}
	rep, err := report.Load(reportPath)
	if err != nil {
		return "", err
	}
	return rep.CommentMarkdown(link)
}

const actionsBot = "github-actions[bot]"

func upsertComment(repo string, pr int, marker, body string) error {
	list := fmt.Sprintf("repos/%s/issues/%d/comments", repo, pr)
	out, err := gh("", "api", "--paginate", list, "--jq", ".[] | {id, body, author: .user.login}")
	if err != nil {
		return err
	}
	self, err := tokenLogin()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return err
	}
	id, err := markedComment(out, report.CommentMarker(marker), self)
	if err != nil {
		return err
	}
	if id == 0 {
		if _, err := gh(string(payload), "api", "--method", "POST", list, "--input", "-"); err != nil {
			return err
		}
		fmt.Printf("commented on %s#%d\n", repo, pr)
		return nil
	}
	if _, err := gh(string(payload), "api", "--method", "PATCH", fmt.Sprintf("repos/%s/issues/comments/%d", repo, id), "--input", "-"); err != nil {
		return err
	}
	fmt.Printf("updated comment %d on %s#%d\n", id, repo, pr)
	return nil
}

func tokenLogin() (string, error) {
	out, err := gh("", "api", "user", "--jq", ".login")
	if err != nil {
		return actionsBot, nil
	}
	return strings.TrimSpace(string(out)), nil
}

func markedComment(list []byte, marker, author string) (int64, error) {
	dec := json.NewDecoder(bytes.NewReader(list))
	var found int64
	for dec.More() {
		var c struct {
			ID     int64
			Body   string
			Author string
		}
		if err := dec.Decode(&c); err != nil {
			return 0, fmt.Errorf("gh api: %w", err)
		}
		if c.Author == author && strings.HasPrefix(c.Body, marker) {
			found = cmp.Or(found, c.ID)
		}
	}
	return found, nil
}
