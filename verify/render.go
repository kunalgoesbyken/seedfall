package verify

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const skipped = "skip"

type row struct {
	status string
	check  string
	result string
}

func (p Proof) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "verify %s (seedfall %s)\n", p.Config, p.Version)
	rows := p.rows()
	width := 0
	for _, r := range rows {
		width = max(width, len(r.check))
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-4s  %-*s  %s\n", r.status, width, r.check, r.result)
	}
	fmt.Fprintln(&b, verdict(p.Passed))
	return b.String()
}

func (p Proof) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### seedfall verify: %s\n\n", verdict(p.Passed))
	fmt.Fprintf(&b, "Config `%s`, seedfall `%s`, reproduce with `%s`\n\n", p.Config, p.Version, p.Command)
	fmt.Fprintln(&b, "| | Check | Result |")
	fmt.Fprintln(&b, "|---|---|---|")
	for _, r := range p.rows() {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", r.status, r.check, strings.ReplaceAll(r.result, "|", `\|`))
	}
	return b.String()
}

func (p Proof) rows() []row {
	var out []row
	for _, rp := range p.Replays {
		out = append(out, row{mark(rp.Failure == nil), "replay " + rp.Target, replayResult(rp)})
	}
	out = append(out, sweepRow("fresh sweep", p.Sweep, cleanSweep))
	out = append(out, sweepRow("determinism", p.Determinism, func(*Sweep) string { return "every seed ran twice with identical host calls and traces" }))
	for _, m := range p.Mutants {
		out = append(out, row{mark(m.Outcome != engine.MutantSurvived), "mutant " + cmp.Or(string(m.Mutation), m.Location), mutantResult(m)})
	}
	if p.MutantError != "" {
		out = append(out, row{mark(false), "source mutants", p.MutantError})
	}
	return out
}

func replayResult(rp Replay) string {
	if rp.Failure != nil {
		return failureText(rp.Failure)
	}
	return "passes, twice, with identical host calls and traces"
}

func sweepRow(name string, s *Sweep, clean func(*Sweep) string) row {
	if s == nil {
		return row{skipped, name, "an earlier check failed"}
	}
	check := fmt.Sprintf("%s, seeds %s", name, s.Seeds)
	if s.Failure != nil {
		return row{mark(false), check, failureText(s.Failure)}
	}
	return row{mark(true), check, clean(s)}
}

func cleanSweep(s *Sweep) string {
	text := fmt.Sprintf("%d of %d pass: 95%% confidence that a failure with rate >= %s would have shown up", s.Passed, s.Count, percent(s.Detectable))
	if s.Rate > 0 {
		text += fmt.Sprintf(" (historical rate %s)", percent(s.Rate))
	}
	return text
}

func mutantResult(m Mutant) string {
	switch {
	case m.Outcome == engine.MutantKilled && m.Change != "":
		return fmt.Sprintf("killed by seed %d: %s", m.Seed, m.Change)
	case m.Outcome == engine.MutantKilled:
		return fmt.Sprintf("killed by seed %d", m.Seed)
	case m.Outcome == engine.MutantSurvived && m.Change != "":
		return "SURVIVED: the checks still pass with " + m.Change
	case m.Outcome == engine.MutantSurvived:
		return "SURVIVED: the checks no longer notice this guarantee disappearing"
	case m.Change != "":
		return string(m.Outcome) + ": " + m.Change
	}
	return string(m.Outcome)
}

func failureText(f *Failure) string {
	return fmt.Sprintf("seed %d FAILS [%s]: %s", f.Seed, f.Classification, f.Reason)
}

func mark(ok bool) string {
	if ok {
		return "pass"
	}
	return "FAIL"
}

func verdict(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}

func percent(f float64) string { return strconv.FormatFloat(f*100, 'g', 3, 64) + "%" }
