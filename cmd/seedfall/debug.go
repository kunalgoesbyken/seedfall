package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const (
	defaultLogLines = 20
	shownNewLines   = 40
	defaultBranch   = 1
)

const debugHelp = `commands:
  where                  virtual time, scheduler step and what each node is doing
  log [n]                the last n trace lines (default 20)
  grep <regex>           every trace line so far that matches
  ls [node]              every node's disk, or one node's: sizes, durable sizes, what a crash loses
  stat <node> <path>     one file's size, durable size, pending byte ranges and damage
  cat <node> <path>      a file's live content, what the node reads now
  net                    in-flight messages and connections not yet accepted
  faults                 active faults and the next scheduled one
  step [n]               run n scheduler steps (default 1)
  until <regex>          run until a new trace line matches
  to <duration>          run until the next step is after this virtual time
  branch [key]           re-randomize everything after now with this key (default 1), as seedfall cause does
  quit                   end the session`

type debugREPL struct {
	s     *engine.Session
	shown int
}

func newDebugREPL(s *engine.Session) *debugREPL {
	return &debugREPL{s: s, shown: len(s.Trace())}
}

func cmdDebug(args []string) error {
	fs := newFlags("debug")
	ca := configFlags(fs)
	seed := seedFlag(fs)
	faultsFile := fs.String("faults", "", "fault schedule from seedfall shrink")
	at := fs.Duration("at", 0, "virtual time to stop at: the session opens before the first step after it")
	netTrace := fs.Bool("net-trace", false, "also trace every connection, message sent and message read")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, _, err := load(ca.ref())
	if err != nil {
		return err
	}
	sched, err := readSchedule(*faultsFile, *seed)
	if err != nil {
		return err
	}
	r.NetTrace = *netTrace
	s, err := r.Debug(*seed, sched, *at)
	if err != nil {
		return err
	}
	defer s.Close()
	d := &debugREPL{s: s}
	fmt.Printf("seed %d, type help for commands\n", *seed)
	d.exec("where", os.Stdout)
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("(seedfall %s) ", s.State().At)
		if !in.Scan() || d.exec(in.Text(), os.Stdout) {
			return in.Err()
		}
	}
}

func (d *debugREPL) exec(line string, w io.Writer) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	cmd, args := fields[0], fields[1:]
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), cmd))
	var err error
	switch cmd {
	case "quit", "exit", "q":
		return true
	case "help", "h", "?":
		fmt.Fprintln(w, debugHelp)
	case "where", "w":
		d.where(w)
	case "log":
		err = d.log(w, args)
	case "grep":
		err = d.grep(w, rest)
	case "ls":
		err = d.ls(w, args)
	case "stat", "cat":
		err = d.file(w, cmd, args)
	case "net":
		d.net(w)
	case "faults":
		d.faults(w)
	case "step", "s":
		err = d.step(w, args)
	case "until":
		err = d.until(w, rest)
	case "to":
		err = d.to(w, args)
	case "branch":
		err = d.branch(w, args)
	default:
		err = fmt.Errorf("unknown command %q, type help", cmd)
	}
	if err != nil {
		fmt.Fprintln(w, "error:", err)
	}
	return false
}

func (d *debugREPL) where(w io.Writer) {
	st := d.s.State()
	switch {
	case st.Over && st.Failed:
		fmt.Fprintf(w, "the run is over: FAIL at t=%s: %s\n", st.At, st.Reason)
	case st.Over:
		fmt.Fprintf(w, "the run is over at t=%s\n", st.At)
	default:
		fmt.Fprintf(w, "paused at t=%s before scheduler step %d\n", st.At, st.Steps+1)
	}
	for _, n := range st.Nodes {
		fmt.Fprintf(w, "  %-12s %-12s %s%s\n", n.Node, n.Instance, n.State, untilNote(n))
	}
	d.shown = len(d.s.Trace())
}

func untilNote(n engine.NodeState) string {
	switch n.State {
	case "paused", "down":
		return " until " + n.Until.String()
	case "runnable":
		return " at " + n.Until.String()
	}
	return ""
}

func (d *debugREPL) log(w io.Writer, args []string) error {
	n := defaultLogLines
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v <= 0 {
			return fmt.Errorf("log takes a positive line count, not %q", args[0])
		}
		n = v
	}
	lines := d.s.Trace()
	for _, l := range lines[max(0, len(lines)-n):] {
		fmt.Fprintln(w, l.Text)
	}
	return nil
}

func (d *debugREPL) grep(w io.Writer, expr string) error {
	re, err := compileArg(expr)
	if err != nil {
		return err
	}
	for _, l := range d.s.Trace() {
		if re.MatchString(l.Text) {
			fmt.Fprintln(w, l.Text)
		}
	}
	return nil
}

func compileArg(expr string) (*regexp.Regexp, error) {
	if expr == "" {
		return nil, errors.New("needs a regular expression")
	}
	return regexp.Compile(expr)
}

func (d *debugREPL) ls(w io.Writer, args []string) error {
	nodes := args
	if len(nodes) == 0 {
		for _, n := range d.s.State().Nodes {
			nodes = append(nodes, n.Node)
		}
	}
	for i, name := range nodes {
		disk, err := d.s.Disk(name)
		if err != nil {
			return err
		}
		if i > 0 {
			fmt.Fprintln(w)
		}
		printDisk(w, disk)
	}
	return nil
}

func (d *debugREPL) file(w io.Writer, cmd string, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("%s takes a node and a path", cmd)
	}
	f, live, err := d.s.File(args[0], args[1])
	if err != nil {
		return err
	}
	if cmd == "cat" {
		_, content := renderContent(live)
		fmt.Fprint(w, content)
		if !strings.HasSuffix(content, "\n") {
			fmt.Fprintln(w)
		}
		return nil
	}
	fmt.Fprintf(w, "%s on %s: %d bytes, %d durable, directory entry durable: %v\n", f.Path, args[0], f.Size, f.DurableSize, f.EntryDurable)
	for _, r := range f.Volatile {
		fmt.Fprintf(w, "  bytes %d-%d are not durable: a crash loses them\n", r.Start, r.End)
	}
	for _, dmg := range f.Damage {
		fmt.Fprintf(w, "  %s at %s: offset %d, %d bytes\n", dmg.Kind, time.Duration(dmg.At), dmg.Offset, dmg.Size)
	}
	return nil
}

func (d *debugREPL) net(w io.Writer) {
	msgs := d.s.InFlight()
	if len(msgs) == 0 {
		fmt.Fprintln(w, "nothing in flight")
		return
	}
	for _, m := range msgs {
		what := m.Kind
		if m.Kind == engine.MessageData {
			what = fmt.Sprintf("%d bytes", m.Bytes)
		}
		sent := ""
		if m.Kind != engine.MessageConnect {
			sent = fmt.Sprintf(", sent at %s", m.Sent)
		}
		fmt.Fprintf(w, "  conn %-4d %s -> %s: %s%s, arrives at %s\n", m.Conn, m.From, m.To, what, sent, m.Arrives)
	}
}

func (d *debugREPL) faults(w io.Writer) {
	active, next := d.s.Faults()
	if len(active) == 0 {
		fmt.Fprintln(w, "no fault is active")
	}
	for _, f := range active {
		fmt.Fprintf(w, "  active: %s\n", strings.TrimSpace(f.Describe()))
	}
	if next != nil {
		fmt.Fprintf(w, "  next:   %s\n", strings.TrimSpace(next.Describe()))
	}
}

func (d *debugREPL) step(w io.Writer, args []string) error {
	n := 1
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v <= 0 {
			return fmt.Errorf("step takes a positive count, not %q", args[0])
		}
		n = v
	}
	return d.ran(w, d.s.Step(n))
}

func (d *debugREPL) until(w io.Writer, expr string) error {
	re, err := compileArg(expr)
	if err != nil {
		return err
	}
	if d.s.Over() {
		return d.ran(w, false)
	}
	if _, ok := d.s.Until(re); !ok {
		fmt.Fprintf(w, "no new trace line matched %q before the run ended\n", expr)
	}
	return d.ran(w, true)
}

func (d *debugREPL) to(w io.Writer, args []string) error {
	if len(args) != 1 {
		return errors.New("to takes a virtual time, for example 1.5s")
	}
	at, err := time.ParseDuration(args[0])
	if err != nil {
		return err
	}
	return d.ran(w, d.s.RunTo(at))
}

func (d *debugREPL) branch(w io.Writer, args []string) error {
	key := uint64(defaultBranch)
	if len(args) > 0 {
		v, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("branch takes a numeric key, not %q", args[0])
		}
		key = v
	}
	if err := d.s.Branch(key); err != nil {
		return err
	}
	fmt.Fprintf(w, "branch %d: the next step re-randomizes the schedule, network, disk and guest randomness\n", key)
	return nil
}

func (d *debugREPL) ran(w io.Writer, moved bool) error {
	if !moved {
		return errors.New("the run is over; inspect it, or quit")
	}
	lines := d.s.Trace()
	fresh := lines[min(d.shown, len(lines)):]
	if len(fresh) > shownNewLines {
		fmt.Fprintf(w, "... %d earlier lines, see log\n", len(fresh)-shownNewLines)
		fresh = fresh[len(fresh)-shownNewLines:]
	}
	for _, l := range fresh {
		fmt.Fprintln(w, l.Text)
	}
	d.where(w)
	return nil
}
