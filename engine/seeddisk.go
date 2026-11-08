package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

type seedFile struct {
	path string
	data []byte
	link string
	dir  bool
}

func (c *Config) loadDisks() error {
	c.disks = map[string][]seedFile{}
	for _, s := range c.all() {
		files, err := c.seedFiles(s)
		if err != nil {
			return fmt.Errorf("config: node %q: %w", s.Name, err)
		}
		if len(files) > 0 {
			c.disks[s.Name] = files
		}
	}
	return nil
}

func (c *Config) seedFiles(s NodeSpec) ([]seedFile, error) {
	byPath := map[string]seedFile{}
	if s.Disk != "" {
		root := resolve(c.Dir, s.Disk)
		info, err := os.Lstat(root)
		if err != nil {
			return nil, fmt.Errorf("disk: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("disk %s is not a directory", s.Disk)
		}
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil || rel == "." {
				return err
			}
			guest := filepath.ToSlash(rel)
			switch {
			case d.IsDir():
				byPath[guest] = seedFile{path: guest, dir: true}
			case d.Type().IsRegular():
				data, err := os.ReadFile(p)
				byPath[guest] = seedFile{path: guest, data: data}
				return err
			case d.Type()&fs.ModeSymlink != 0:
				target, err := os.Readlink(p)
				byPath[guest] = seedFile{path: guest, link: filepath.ToSlash(target)}
				return err
			default:
				return fmt.Errorf("disk %s: %s is not a regular file, directory or symlink", s.Disk, rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for _, guest := range sortedKeys(s.Files) {
		p := clean(guest)
		if p == "." {
			return nil, fmt.Errorf("files: %q is not a file path", guest)
		}
		data, err := os.ReadFile(resolve(c.Dir, s.Files[guest]))
		if err != nil {
			return nil, fmt.Errorf("files: %w", err)
		}
		byPath[p] = seedFile{path: p, data: data}
	}
	files := make([]seedFile, 0, len(byPath))
	for _, p := range sortedKeys(byPath) {
		if i := slices.IndexFunc(files, func(o seedFile) bool { return !o.dir && strings.HasPrefix(p, o.path+"/") }); i >= 0 {
			return nil, fmt.Errorf("%s is inside the file %s", p, files[i].path)
		}
		files = append(files, byPath[p])
	}
	return files, nil
}

func (c *Config) Digest() string {
	h := sha256.New()
	h.Write(c.resolved)
	for _, name := range sortedKeys(c.disks) {
		for _, f := range c.disks[name] {
			fmt.Fprintf(h, "\x00%s\x00%s\x00%t\x00%s\x00%d\x00", name, f.path, f.dir, f.link, len(f.data))
			h.Write(f.data)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m *memFS) seed(files []seedFile) (int, int64) {
	var n int
	var size int64
	for _, f := range files {
		for _, d := range parents(f.path) {
			m.seedDir(d)
		}
		if f.dir {
			m.seedDir(f.path)
			continue
		}
		in := m.newInode()
		in.data, in.durable, in.target, in.synced = slices.Clone(f.data), slices.Clone(f.data), f.link, true
		m.files[f.path] = in
		if m.ns != nil {
			m.ns.files[f.path] = in
		}
		n++
		size += int64(len(f.data))
	}
	return n, size
}

func (m *memFS) seedDir(p string) {
	if _, ok := m.dirs[p]; ok {
		return
	}
	m.dirs[p] = m.allocIno()
	if m.ns != nil {
		m.ns.dirs[p] = m.dirs[p]
	}
}

func parents(p string) []string {
	var out []string
	for d := path.Dir(p); d != "."; d = path.Dir(d) {
		out = append([]string{d}, out...)
	}
	return out
}
