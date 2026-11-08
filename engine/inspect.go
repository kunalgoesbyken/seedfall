package engine

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type ByteRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type Tear struct {
	At     Duration `json:"at"`
	Offset int64    `json:"offset"`
	Kept   int64    `json:"kept"`
	Size   int64    `json:"size"`
}

type WriteFate struct {
	Offset   int64 `json:"offset"`
	Size     int64 `json:"size"`
	Kept     int64 `json:"kept"`
	Truncate bool  `json:"truncate,omitempty"`
	Survived bool  `json:"survived"`
}

type Writeback struct {
	At     Duration    `json:"at"`
	Writes []WriteFate `json:"writes"`
}

type Damage struct {
	At     Duration `json:"at"`
	Kind   string   `json:"kind"`
	Offset int64    `json:"offset"`
	Size   int64    `json:"size"`
	Target int64    `json:"target,omitempty"`
}

type FileState struct {
	Path         string      `json:"path"`
	Size         int64       `json:"size"`
	DurableSize  int64       `json:"durable_size"`
	Volatile     []ByteRange `json:"volatile,omitempty"`
	EntryDurable bool        `json:"entry_durable"`
	Tear         *Tear       `json:"last_tear,omitempty"`
	Writeback    *Writeback  `json:"last_writeback,omitempty"`
	Damage       []Damage    `json:"damage,omitempty"`
	Durable      []byte      `json:"-"`
}

func (f FileState) VolatileBytes() int64 {
	var n int64
	for _, r := range f.Volatile {
		n += r.End - r.Start
	}
	return n
}

type DirState struct {
	Path    string `json:"path"`
	Durable bool   `json:"durable"`
}

type Disk struct {
	Node    string      `json:"node"`
	Strict  bool        `json:"strict_dirs"`
	Torn    bool        `json:"torn_writes"`
	Reorder bool        `json:"reorder_writes"`
	Bytes   int64       `json:"disk_bytes,omitempty"`
	Used    int64       `json:"used_bytes"`
	Dirs    []DirState  `json:"dirs"`
	Files   []FileState `json:"files"`
	Ghosts  []FileState `json:"ghosts,omitempty"`
}

func (d Disk) AfterCrash() ([]DirState, []FileState) {
	dirs := slices.DeleteFunc(slices.Clone(d.Dirs), func(s DirState) bool { return !s.Durable })
	files := slices.DeleteFunc(slices.Clone(d.Files), func(f FileState) bool { return !f.EntryDurable })
	files = append(files, d.Ghosts...)
	slices.SortFunc(files, func(a, b FileState) int { return strings.Compare(a.Path, b.Path) })
	return dirs, files
}

type InspectOptions struct {
	At   time.Duration
	Node string
}

type Inspection struct {
	Result Result
	At     time.Duration
	Disks  []Disk
}

type probe struct {
	at    int64
	node  string
	taken bool
	when  int64
	disks []Disk
}

func (r *Runner) Inspect(seed uint64, s Schedule, opts InspectOptions) (Inspection, error) {
	if opts.At < 0 {
		return Inspection{}, fmt.Errorf("inspect time %s is negative", opts.At)
	}
	if opts.Node != "" && !slices.ContainsFunc(r.cfg.all(), func(s NodeSpec) bool { return s.Name == opts.Node }) {
		return Inspection{}, fmt.Errorf("unknown node %q", opts.Node)
	}
	p := &probe{at: int64(opts.At), node: opts.Node}
	if opts.At == 0 {
		p.at = never
	}
	s, explicit := r.schedule(seed, s)
	res := r.run(seed, s, explicit, nil, p, 0)
	return Inspection{Result: res, At: time.Duration(p.when), Disks: p.disks}, nil
}

func (w *world) captureBefore(at int64) {
	if w.probe != nil && at > w.probe.at {
		w.capture()
	}
}

func (w *world) capture() {
	p := w.probe
	if p == nil || p.taken {
		return
	}
	p.taken, p.when = true, w.now
	for _, n := range w.nodes {
		if p.node == "" || p.node == n.name() {
			d := n.fs.snapshot(n.name())
			d.Torn, d.Reorder = w.spec.TornWrites, w.spec.ReorderWrites
			p.disks = append(p.disks, d)
		}
	}
}

func (m *memFS) snapshot(node string) Disk {
	d := Disk{Node: node, Strict: m.ns != nil, Bytes: m.capacity, Used: m.used()}
	for _, p := range sortedKeys(m.dirs) {
		if p != "." {
			d.Dirs = append(d.Dirs, DirState{Path: p, Durable: m.dirDurable(p)})
		}
	}
	for _, p := range sortedKeys(m.files) {
		d.Files = append(d.Files, fileState(p, m.files[p], m.entryDurable(p, m.files[p])))
	}
	if m.ns != nil {
		for _, p := range sortedKeys(m.ns.files) {
			if in := m.ns.files[p]; m.files[p] != in && m.ns.reachable(p) {
				d.Ghosts = append(d.Ghosts, fileState(p, in, true))
			}
		}
	}
	return d
}

func (m *memFS) entryDurable(p string, in *inode) bool {
	if m.ns == nil {
		return in.synced
	}
	return m.ns.files[p] == in && m.ns.reachable(p)
}

func (m *memFS) dirDurable(p string) bool {
	if m.ns == nil {
		return true
	}
	_, ok := m.ns.dirs[p]
	return ok && m.ns.reachable(p)
}

func fileState(p string, in *inode, entryDurable bool) FileState {
	f := FileState{
		Path: p, Size: int64(len(in.data)), DurableSize: int64(len(in.durable)),
		Volatile: diffRanges(in.data, in.durable), EntryDurable: entryDurable,
		Durable: slices.Clone(in.durable), Damage: slices.Clone(in.damage),
	}
	if in.tear != nil {
		t := *in.tear
		f.Tear = &t
	}
	if in.wb != nil {
		wb := *in.wb
		wb.Writes = slices.Clone(wb.Writes)
		f.Writeback = &wb
	}
	return f
}

func diffRanges(live, durable []byte) []ByteRange {
	var out []ByteRange
	for i := range live {
		if i < len(durable) && live[i] == durable[i] {
			continue
		}
		if n := len(out); n > 0 && out[n-1].End == int64(i) {
			out[n-1].End++
			continue
		}
		out = append(out, ByteRange{Start: int64(i), End: int64(i) + 1})
	}
	return out
}

func (d Disk) DurableFile(p string) (FileState, bool) {
	_, files := d.AfterCrash()
	i := slices.IndexFunc(files, func(f FileState) bool { return f.Path == clean(p) })
	if i < 0 {
		return FileState{}, false
	}
	return files[i], true
}

func ExportDisks(disks []Disk, dir string) ([]string, error) {
	var roots []string
	for _, d := range disks {
		if !filepath.IsLocal(d.Node) {
			return roots, fmt.Errorf("node name %q cannot be used as a directory name", d.Node)
		}
		root := filepath.Join(dir, d.Node)
		if err := d.export(root); err != nil {
			return roots, err
		}
		roots = append(roots, root)
	}
	return roots, nil
}

func (d Disk) export(root string) error {
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
