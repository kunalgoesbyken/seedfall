package report

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Entry struct {
	Time     time.Time      `json:"time"`
	Config   string         `json:"config"`
	Seeds    int            `json:"seeds"`
	Version  string         `json:"seedfall_version,omitempty"`
	Findings map[string]int `json:"findings"`
}

type known struct {
	failures   int
	seeds      int
	cleanSeeds int
}

func (k known) seedsToConfirm() int { return SeedsToConfirm(float64(k.failures) / float64(k.seeds)) }

func (k known) resolved(extraCleanSeeds int) bool {
	return k.cleanSeeds+extraCleanSeeds >= k.seedsToConfirm()
}

func knownFindings(entries []Entry) map[string]known {
	out := map[string]known{}
	for _, e := range entries {
		for key, k := range out {
			if e.Findings[key] == 0 {
				k.cleanSeeds += e.Seeds
				out[key] = k
			}
		}
		for key, count := range e.Findings {
			if count > 0 {
				out[key] = known{failures: count, seeds: e.Seeds}
			}
		}
	}
	return out
}

const EventNotified = "notified"

type Event struct {
	Time    time.Time `json:"time"`
	Config  string    `json:"config"`
	Kind    string    `json:"event"`
	Finding string    `json:"finding"`
	Report  time.Time `json:"report"`
}

type record struct {
	Entry
	Kind string `json:"event"`
}

func ReadHistory(path, config string) ([]Entry, error) {
	var entries []Entry
	err := readLines(path, func(b []byte) error {
		var r record
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		if r.Kind == "" && r.Config == config {
			entries = append(entries, r.Entry)
		}
		return nil
	})
	return entries, err
}

func ReadEvents(path, config string) ([]Event, error) {
	var events []Event
	err := readLines(path, func(b []byte) error {
		var e Event
		if err := json.Unmarshal(b, &e); err != nil {
			return err
		}
		if e.Kind != "" && e.Config == config {
			events = append(events, e)
		}
		return nil
	})
	return events, err
}

func readLines(path string, line func([]byte) error) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for n := 1; scanner.Scan(); n++ {
		b := bytes.TrimSpace(scanner.Bytes())
		if len(b) == 0 {
			continue
		}
		if err := line(b); err != nil {
			return fmt.Errorf("%s line %d: %w", path, n, err)
		}
	}
	return scanner.Err()
}

func AppendEvent(path string, e Event) error { return appendLine(path, e) }

func appendLine(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(v)
}
