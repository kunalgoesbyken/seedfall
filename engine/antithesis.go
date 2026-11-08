package engine

import (
	"bytes"
	"encoding/json"

	fsapi "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/sys"
)

const (
	antithesisOutputEnv  = "ANTITHESIS_SDK_LOCAL_OUTPUT"
	antithesisOutputPath = ".seedfall/antithesis.jsonl"
)

type antithesisAssert struct {
	AssertType string          `json:"assert_type"`
	Message    string          `json:"message"`
	Hit        bool            `json:"hit"`
	MustHit    bool            `json:"must_hit"`
	Condition  bool            `json:"condition"`
	Details    json.RawMessage `json:"details"`
}

type antithesisGuidance struct {
	GuidanceType string `json:"guidance_type"`
	Message      string `json:"message"`
	Maximize     bool   `json:"maximize"`
	Hit          bool   `json:"hit"`
	Data         *struct {
		Left  float64 `json:"left"`
		Right float64 `json:"right"`
	} `json:"guidance_data"`
}

type antithesisEvent struct {
	Assert   *antithesisAssert   `json:"antithesis_assert"`
	Guidance *antithesisGuidance `json:"antithesis_guidance"`
	Setup    *struct {
		Status string `json:"status"`
	} `json:"antithesis_setup"`
}

var antithesisKeys = map[string]bool{"antithesis_assert": true, "antithesis_guidance": true, "antithesis_setup": true, "antithesis_sdk": true}

func (a antithesisAssert) kind() (PropertyKind, bool) {
	switch {
	case a.AssertType == "always" && a.MustHit:
		return Always, true
	case a.AssertType == "always":
		return AlwaysOrUnreachable, true
	case a.AssertType == "sometimes":
		return Sometimes, true
	case a.AssertType == "reachability" && a.MustHit:
		return Reachable, true
	case a.AssertType == "reachability":
		return Unreachable, true
	}
	return 0, false
}

func (a antithesisAssert) detail() string {
	if len(a.Details) == 0 || string(a.Details) == "null" {
		return ""
	}
	return string(a.Details)
}

type antithesisFile struct {
	fsapi.UnimplementedFile
	w       *world
	n       *node
	pending []byte
}

func (w *world) openSpecial(n *node, p string) (fsapi.File, fsapi.Errno, bool) {
	if p == antithesisOutputPath {
		return &antithesisFile{w: w, n: n}, 0, true
	}
	return w.openNetFile(n, p)
}

func (f *antithesisFile) Stat() (sys.Stat_t, fsapi.Errno) { return sys.Stat_t{Mode: 0o644}, 0 }
func (f *antithesisFile) IsDir() (bool, fsapi.Errno)      { return false, 0 }
func (f *antithesisFile) Close() fsapi.Errno              { return 0 }
func (f *antithesisFile) Sync() fsapi.Errno               { return 0 }
func (f *antithesisFile) IsAppend() bool                  { return true }
func (f *antithesisFile) SetAppend(bool) fsapi.Errno      { return 0 }

func (f *antithesisFile) Write(buf []byte) (int, fsapi.Errno) {
	f.pending = append(f.pending, buf...)
	for {
		i := bytes.IndexByte(f.pending, '\n')
		if i < 0 {
			return len(buf), 0
		}
		line := f.pending[:i]
		f.pending = f.pending[i+1:]
		f.w.antithesisLine(f.n, line)
	}
}

func (w *world) antithesisLine(n *node, line []byte) {
	var fields map[string]json.RawMessage
	var ev antithesisEvent
	if json.Unmarshal(line, &fields) != nil || json.Unmarshal(line, &ev) != nil || n.live == nil {
		return
	}
	if ev.Setup != nil && ev.Setup.Status == "complete" {
		w.setupComplete(n.live)
	}
	w.antithesisGuidance(ev.Guidance)
	for name, details := range fields {
		if len(fields) == 1 && !antithesisKeys[name] {
			w.event(n.live, name, string(details))
		}
	}
	if ev.Assert == nil || !ev.Assert.Hit {
		return
	}
	if kind, ok := ev.Assert.kind(); ok {
		w.observeProperty(n.live, kind, ev.Assert.Condition, ev.Assert.Message, ev.Assert.detail())
	}
}

func (w *world) antithesisGuidance(g *antithesisGuidance) {
	if g == nil || !g.Hit || g.GuidanceType != "numeric" || g.Data == nil {
		return
	}
	prop, ok := w.props[g.Message]
	if !ok {
		return
	}
	margin := g.Data.Left - g.Data.Right
	if g.Maximize != prop.Kind.needsWitness() {
		margin = g.Data.Right - g.Data.Left
	}
	w.props.margin(prop.Kind, g.Message, margin)
}
