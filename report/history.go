package report

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Entry struct {
	Time     time.Time      `json:"time"`
	Config   string         `json:"config"`
	Seeds    int            `json:"seeds"`
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

func readHistory(path, config string) ([]Entry, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var e Entry
		if json.Unmarshal(scanner.Bytes(), &e) == nil && e.Config == config {
			entries = append(entries, e)
		}
	}
	return entries, scanner.Err()
}

func appendEntry(path string, e Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e)
}
