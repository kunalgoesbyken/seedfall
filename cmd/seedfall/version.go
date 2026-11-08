package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

var flagAliases = map[string]string{"-h": "help", "-help": "help", "--help": "help", "-version": "version", "--version": "version"}

func normalizeArgs(args []string) []string {
	if len(args) > 0 {
		if name, ok := flagAliases[args[0]]; ok {
			return append([]string{name}, args[1:]...)
		}
	}
	return args
}

func cmdVersion(args []string) error {
	fs := newFlags("version")
	jsonOut := fs.Bool("json", false, "print the build as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	info := engine.Binary()
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(info)
	}
	fmt.Printf("seedfall %s\n", info.Version)
	if info.Commit != "" {
		fmt.Printf("commit:   %s\n", info.Commit)
	}
	if info.Date != "" {
		fmt.Printf("built:    %s\n", info.Date)
	}
	fmt.Printf("go:       %s %s\n", info.GoVersion, info.Platform)
	return nil
}

func cmdHelp(args []string) error {
	fs := newFlags("help")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fmt.Print(usage())
		return nil
	}
	cmd, rest, ok := findCommand(fs.Args())
	if !ok || len(rest) > 0 {
		return fmt.Errorf("unknown command %q; seedfall help lists them", strings.Join(fs.Args(), " "))
	}
	text, err := commandHelp(cmd)
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}

func commandHelp(c command) (string, error) {
	flags, err := commandFlags(c)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "usage: seedfall %s %s\n", c.name, c.synopsis)
	if len(flags) > 0 {
		b.WriteString("\nflags:\n")
	}
	for _, f := range flags {
		fmt.Fprintf(&b, "  -%s", f.Name)
		if f.Type != "bool" {
			fmt.Fprintf(&b, " %s", f.Type)
		}
		fmt.Fprintf(&b, "\n        %s", f.Usage)
		if !slices.Contains([]string{"", "0", "false", "0s"}, f.Default) {
			fmt.Fprintf(&b, " (default %s)", f.Default)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

const etaUnknown = "?"

type progressLine struct {
	w     io.Writer
	width int
}

func formatProgress(p engine.SweepProgress) string {
	eta := etaUnknown
	if p.Done > 0 {
		eta = p.Remaining().Round(time.Second).String()
	}
	return fmt.Sprintf("%d/%d seeds  %.1f/s  %d failed  eta %s  %.0fx virtual", p.Done, p.Total, p.SeedsPerSecond(), p.Failed, eta, p.VirtualPerWall())
}

func (l *progressLine) update(p engine.SweepProgress) {
	text := formatProgress(p)
	fmt.Fprintf(l.w, "\r%s%s", text, strings.Repeat(" ", max(0, l.width-len(text))))
	l.width = len(text)
}

func (l *progressLine) clear() {
	if l.width > 0 {
		fmt.Fprintf(l.w, "\r%s\r", strings.Repeat(" ", l.width))
	}
}

func stderrIsTerminal() bool {
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func withProgress(opts *engine.SweepOptions, enabled bool) func() {
	if !enabled {
		return func() {}
	}
	line := &progressLine{w: os.Stderr}
	opts.Progress = line.update
	return line.clear
}
