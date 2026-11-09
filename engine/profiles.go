package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type ConfigRef struct {
	Path    string
	Profile string
	Sets    []string
}

func (r ConfigRef) String() string {
	parts := []string{shellQuote(r.Path)}
	if r.Profile != "" {
		parts = append(parts, "-profile", shellQuote(r.Profile))
	}
	for _, s := range r.Sets {
		parts = append(parts, "-set", shellQuote(s))
	}
	return strings.Join(parts, " ")
}

const setsDigestBytes = 4

func setsDigest(sets []string) string {
	sum := sha256.Sum256([]byte(strings.Join(sets, "\x00")))
	return hex.EncodeToString(sum[:setsDigestBytes])
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !shellSafe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellSafe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=,+@%", r)
}

func resolveLayers(ref ConfigRef) (map[string]any, error) {
	doc, err := readLayers(ref.Path, nil)
	if err != nil {
		return nil, err
	}
	profiles := doc["profiles"]
	delete(doc, "profiles")
	if ref.Profile != "" {
		overlay, err := profile(profiles, ref.Profile)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ref.Path, err)
		}
		doc = mergeLayer(doc, overlay)
	}
	for _, s := range ref.Sets {
		if err := setPath(doc, s); err != nil {
			return nil, err
		}
		normalizeFaults(doc)
	}
	return doc, nil
}

func fits(doc map[string]any) bool {
	b, err := json.Marshal(doc)
	return err == nil && decodeConfig(b, &Config{}) == nil
}

func normalizeFaults(doc map[string]any) {
	if name, ok := doc["faults"].(string); ok {
		doc["faults"] = map[string]any{"preset": name}
	}
}

func mergeLayer(base, over map[string]any) map[string]any {
	normalizeFaults(over)
	if f, ok := over["faults"].(map[string]any); ok && f["preset"] != nil {
		delete(base, "faults")
	}
	return merge(base, over)
}

func readLayers(path string, seen []string) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if slices.Contains(seen, abs) {
		return nil, fmt.Errorf("config: %s extends itself", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := decodeJSON(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if doc == nil {
		return nil, fmt.Errorf("%s: config must be a JSON object", path)
	}
	normalizeFaults(doc)
	parent, ok := doc["extends"]
	delete(doc, "extends")
	if !ok {
		return doc, nil
	}
	name, ok := parent.(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("%s: extends must be the path of another config file", path)
	}
	base, err := readLayers(resolve(filepath.Dir(path), name), append(seen, abs))
	if err != nil {
		return nil, err
	}
	return mergeLayer(base, doc), nil
}

func decodeJSON(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected data after the JSON value")
	}
	return nil
}

func profile(profiles any, name string) (map[string]any, error) {
	all, _ := profiles.(map[string]any)
	overlay, ok := all[name].(map[string]any)
	if !ok {
		names := sortedKeys(all)
		if len(names) == 0 {
			return nil, fmt.Errorf("config: no profile %q, the config has no profiles", name)
		}
		return nil, fmt.Errorf("config: no profile %q, have %s", name, strings.Join(names, ", "))
	}
	return overlay, nil
}

func merge(base, over map[string]any) map[string]any {
	for k, v := range over {
		base[k] = mergeValue(base[k], v)
	}
	return base
}

func mergeValue(base, over any) any {
	switch o := over.(type) {
	case map[string]any:
		if b, ok := base.(map[string]any); ok {
			return merge(b, o)
		}
	case []any:
		if b, ok := base.([]any); ok && named(b) && named(o) {
			return mergeNamed(b, o)
		}
	}
	return over
}

func named(list []any) bool {
	return !slices.ContainsFunc(list, func(v any) bool {
		_, ok := elementName(v)
		return !ok
	})
}

func elementName(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	name, ok := m["name"].(string)
	return name, ok && name != ""
}

func mergeNamed(base, over []any) []any {
	for _, v := range over {
		name, _ := elementName(v)
		if i := indexByName(base, name); i >= 0 {
			base[i] = mergeValue(base[i], v)
			continue
		}
		base = append(base, v)
	}
	return base
}

func indexByName(list []any, name string) int {
	return slices.IndexFunc(list, func(v any) bool {
		n, _ := elementName(v)
		return n == name
	})
}

func setPath(doc map[string]any, expr string) error {
	key, raw, ok := strings.Cut(expr, "=")
	if !ok || key == "" {
		return fmt.Errorf("config: -set %q is not path=value", expr)
	}
	var value any
	if decodeJSON([]byte(raw), &value) != nil {
		value = raw
	}
	if err := assign(doc, key, value); err != nil || fits(doc) {
		return err
	}
	if _, isString := value.(string); !isString {
		if err := assign(doc, key, raw); err != nil {
			return err
		}
		if !fits(doc) {
			return assign(doc, key, value)
		}
	}
	return nil
}

func assign(doc map[string]any, key string, value any) error {
	segs := strings.Split(key, ".")
	var cur any = doc
	for i, seg := range segs {
		last := i == len(segs)-1
		switch c := cur.(type) {
		case map[string]any:
			if last {
				c[seg] = value
				return nil
			}
			if next, ok := c[seg].(map[string]any); ok {
				cur = next
				continue
			}
			if next, ok := c[seg].([]any); ok {
				cur = next
				continue
			}
			next := map[string]any{}
			c[seg] = next
			cur = next
		case []any:
			j, err := listIndex(c, seg)
			if err != nil {
				return fmt.Errorf("config: -set %s: %s: %w", key, strings.Join(segs[:i], "."), err)
			}
			if last {
				c[j] = value
				return nil
			}
			cur = c[j]
		default:
			return fmt.Errorf("config: -set %s: %s is not an object or a list", key, strings.Join(segs[:i], "."))
		}
	}
	return nil
}

func listIndex(list []any, seg string) (int, error) {
	if i := indexByName(list, seg); i >= 0 {
		return i, nil
	}
	i, err := strconv.Atoi(seg)
	if err != nil || i < 0 || i >= len(list) {
		return 0, fmt.Errorf("no element named or numbered %q", seg)
	}
	return i, nil
}
