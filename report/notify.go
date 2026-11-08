package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	messageLimit   = 2000
	webhookTimeout = 30 * time.Second
)

func (r Report) Alerts(sent []Event) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Status != StatusNew && f.Status != StatusRegressed {
			continue
		}
		if slices.ContainsFunc(sent, func(e Event) bool {
			return e.Kind == EventNotified && e.Finding == f.Key && e.Report.Equal(r.Generated)
		}) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func (r Report) Message(findings []Finding, link string) string {
	head := fmt.Sprintf("seedfall: %d new or regressed findings in %s (seeds %s, %d of %d failed)", len(findings), r.Config, r.Seeds, r.Failed, r.Total)
	if link != "" {
		head += "\n" + link
	}
	var b strings.Builder
	b.WriteString(head)
	for i, f := range findings {
		line := fmt.Sprintf("\n• %s %s: %d seeds (%s)", f.Status, f.Key, f.Count, percent(f.Rate))
		if f.Example != nil {
			line += fmt.Sprintf(", e.g. seed %d: %s\n  %s", f.Example.Seed, f.Example.Reason, f.Example.Replay)
		}
		more := fmt.Sprintf("\n… and %d more", len(findings)-i)
		if b.Len()+len(line)+len(more) > messageLimit {
			b.WriteString(more)
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

func Post(webhook, text string) error {
	body, err := json.Marshal(map[string]string{"text": text, "content": text})
	if err != nil {
		return err
	}
	client := http.Client{Timeout: webhookTimeout}
	resp, err := client.Post(webhook, "application/json", bytes.NewReader(body))
	var hidden *url.Error
	if errors.As(err, &hidden) {
		return fmt.Errorf("webhook: %w", hidden.Err)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, messageLimit))
		return fmt.Errorf("webhook answered %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}
	return nil
}
