package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kunalgoesbyken/seedfall/engine"
)

const (
	shownRanges = 3
	pathWidth   = 50
)

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	conf := fs.String("c", "seedfall.json", "config file")
	seed := seedFlag(fs)
	faultsFile := fs.String("faults", "", "fault schedule from seedfall shrink")
	at := fs.Duration("at", 0, "virtual time to snapshot the disks at; 0 means the moment of failure")
	node := fs.String("node", "", "only inspect this node")
	cat := fs.String("cat", "", "print the durable content of this file, as text or a hex dump")
	out := fs.String("out", "", "export each node's durable disk image into this directory")
	fs.Parse(args)
	if *cat != "" && *out != "" {
		return errors.New("-cat and -out cannot be combined")
	}
	r, _, err := load(*conf)
	if err != nil {
		return err
	}
	faults, err := readFaults(*faultsFile)
	if err != nil {
		return err
	}
	in, err := r.Inspect(*seed, faults, engine.InspectOptions{At: *at, Node: *node})
	if err != nil {
		return err
	}
	if *cat != "" {
		return printDurable(in.Disks, *cat)
	}
	printInspection(in, *at)
	if *out != "" {
		return exportImages(in.Disks, *out)
	}
	return nil
}

func printInspection(in engine.Inspection, at time.Duration) {
	res := in.Result
	if res.Failed {
		fmt.Printf("seed %d failed at t=%s: %s\n", res.Seed, res.At, res.Reason)
	} else {
		fmt.Printf("seed %d passed (%s)\n", res.Seed, status(res))
	}
	fmt.Printf("disk state at t=%s, %s\n", in.At, snapshotMoment(res, at))
	for _, d := range in.Disks {
		fmt.Println()
		printDisk(d)
	}
}

func snapshotMoment(res engine.Result, at time.Duration) string {
	switch {
	case at > 0 && res.Failed && res.At <= at:
		return "the moment of failure (before -at)"
	case at > 0:
		return fmt.Sprintf("the last step at or before -at %s", at)
	case res.Failed:
		return "the moment of failure"
	}
	return "the end of the run"
}

func printDisk(d engine.Disk) {
	mode := "directory entries are durable once made"
	if d.Strict {
		mode = "strict_dirs: directory entries are durable only after their parent directory is fsynced"
	}
	if d.Torn {
		mode += "; torn_writes: a crash may also keep a prefix of the pending writes"
	}
	fmt.Printf("node %s (%s)\n", d.Node, mode)
	if len(d.Dirs)+len(d.Files)+len(d.Ghosts) == 0 {
		fmt.Println("  empty disk")
		return
	}
	fmt.Printf("  %-*s %10s %10s  %s\n", pathWidth-2, "path", "size", "durable", "state")
	newDiskTree(d).print(".", 1)
	if len(d.Ghosts) > 0 {
		fmt.Println("  removed or renamed away, but a crash brings them back (parent directory not fsynced):")
		for _, f := range d.Ghosts {
			fmt.Printf("    %-*s %10s %10d\n", pathWidth-4, f.Path, "-", f.DurableSize)
		}
	}
	printTotals(d)
}

func printTotals(d engine.Disk) {
	var durable, pending int64
	lost := 0
	for _, f := range d.Files {
		pending += f.VolatileBytes()
		if f.EntryDurable {
			durable += f.DurableSize
			continue
		}
		lost++
	}
	for _, s := range d.Dirs {
		if !s.Durable {
			lost++
		}
	}
	fmt.Printf("  %d dirs, %d files: %d bytes durable, %d bytes pending, %d entries lost on crash, %d removed entries that come back\n", len(d.Dirs), len(d.Files), durable, pending, lost, len(d.Ghosts))
}

type diskTree struct {
	strict   bool
	children map[string][]string
	dirs     map[string]engine.DirState
	files    map[string]engine.FileState
}

func newDiskTree(d engine.Disk) diskTree {
	t := diskTree{strict: d.Strict, children: map[string][]string{}, dirs: map[string]engine.DirState{}, files: map[string]engine.FileState{}}
	for _, s := range d.Dirs {
		t.dirs[s.Path] = s
		t.children[path.Dir(s.Path)] = append(t.children[path.Dir(s.Path)], s.Path)
	}
	for _, f := range d.Files {
		t.files[f.Path] = f
		t.children[path.Dir(f.Path)] = append(t.children[path.Dir(f.Path)], f.Path)
	}
	for _, c := range t.children {
		sort.Strings(c)
	}
	return t
}

func (t diskTree) print(dir string, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, p := range t.children[dir] {
		if s, ok := t.dirs[p]; ok {
			fmt.Printf("%s%-*s %10s %10s  %s\n", indent, pathWidth-len(indent), path.Base(p)+"/", "-", "-", t.dirNote(s))
			t.print(p, depth+1)
			continue
		}
		f := t.files[p]
		fmt.Printf("%s%-*s %10d %10d  %s\n", indent, pathWidth-len(indent), path.Base(p), f.Size, f.DurableSize, t.fileNote(f))
		if f.Tear != nil {
			fmt.Printf("%s  ^ torn by the crash at t=%s: kept %d of a %d-byte write at offset %d\n", indent, time.Duration(f.Tear.At), f.Tear.Kept, f.Tear.Size, f.Tear.Offset)
		}
	}
}

func (t diskTree) lostNote() string {
	if t.strict {
		return "NOT DURABLE: entry vanishes on crash, parent directory not fsynced"
	}
	return "NOT DURABLE: never fsynced, vanishes on crash"
}

func (t diskTree) dirNote(s engine.DirState) string {
	if s.Durable {
		return "durable"
	}
	return t.lostNote()
}

func (t diskTree) fileNote(f engine.FileState) string {
	if !f.EntryDurable {
		return t.lostNote()
	}
	var notes []string
	if n := f.VolatileBytes(); n > 0 {
		notes = append(notes, fmt.Sprintf("PENDING: %d bytes lost on crash at %s", n, formatRanges(f.Volatile)))
	}
	if f.DurableSize > f.Size {
		notes = append(notes, fmt.Sprintf("PENDING truncate: a crash restores %d bytes", f.DurableSize-f.Size))
	}
	if len(notes) == 0 {
		return "durable"
	}
	return strings.Join(notes, "; ")
}

func formatRanges(ranges []engine.ByteRange) string {
	parts := make([]string, 0, shownRanges+1)
	for _, r := range ranges[:min(len(ranges), shownRanges)] {
		parts = append(parts, fmt.Sprintf("[%d,%d)", r.Start, r.End))
	}
	if extra := len(ranges) - shownRanges; extra > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", extra))
	}
	return strings.Join(parts, " ")
}

func durableFile(disks []engine.Disk, p string) (engine.FileState, error) {
	var found []engine.FileState
	var nodes []string
	for _, d := range disks {
		if f, ok := d.DurableFile(p); ok {
			found, nodes = append(found, f), append(nodes, d.Node)
		}
	}
	switch len(found) {
	case 0:
		return engine.FileState{}, fmt.Errorf("%s has no durable content: it does not exist or would not survive a crash", p)
	case 1:
		return found[0], nil
	}
	return engine.FileState{}, fmt.Errorf("%s exists on nodes %s, pick one node", p, strings.Join(nodes, ", "))
}

func printDurable(disks []engine.Disk, p string) error {
	f, err := durableFile(disks, p)
	if err != nil {
		return err
	}
	_, content := renderContent(f.Durable)
	fmt.Print(content)
	return nil
}

func renderContent(b []byte) (string, string) {
	binary := func(r rune) bool { return unicode.IsControl(r) && !unicode.IsSpace(r) }
	if utf8.Valid(b) && !strings.ContainsFunc(string(b), binary) {
		return "text", string(b)
	}
	return "hex", hex.Dump(b)
}

func exportImages(disks []engine.Disk, dir string) error {
	for _, d := range disks {
		if !filepath.IsLocal(d.Node) {
			return fmt.Errorf("node name %q cannot be used as a directory name", d.Node)
		}
		root := filepath.Join(dir, d.Node)
		if err := exportImage(d, root); err != nil {
			return err
		}
		fmt.Printf("\nexported the durable disk image of node %s to %s\n", d.Node, root)
	}
	return nil
}

func exportImage(d engine.Disk, root string) error {
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		return fmt.Errorf("export target must not exist yet: %w", err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	dirs, files := d.AfterCrash()
	for _, s := range dirs {
		if err := dir.MkdirAll(s.Path, 0o755); err != nil {
			return err
		}
	}
	for _, f := range files {
		if err := dir.MkdirAll(path.Dir(f.Path), 0o755); err != nil {
			return err
		}
		if err := dir.WriteFile(f.Path, f.Durable, 0o644); err != nil {
			return err
		}
	}
	return nil
}
