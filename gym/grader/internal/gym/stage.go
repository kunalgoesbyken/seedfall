package gym

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const seedfallModule = "github.com/kunalgoesbyken/seedfall"

func SeedfallSource(flag string) (string, error) {
	if flag != "" {
		return filepath.Abs(flag)
	}
	if env := os.Getenv("SEEDFALL_SRC"); env != "" {
		return filepath.Abs(env)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if declaresModule(filepath.Join(dir, "go.mod"), seedfallModule) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("locate %s: run inside the seedfall repository, pass -seedfall or set SEEDFALL_SRC", seedfallModule)
		}
		dir = parent
	}
}

func declaresModule(goMod, module string) bool {
	data, err := os.ReadFile(goMod)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			return fields[1] == module
		}
	}
	return false
}

func Stage(task Task, workspace, seedfallSrc string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "gym-"+task.ID+"-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	fail := func(err error) (string, func(), error) {
		cleanup()
		return "", nil, err
	}
	if err := copyTree(task.Harness(), dir); err != nil {
		return fail(err)
	}
	for _, e := range task.Editable {
		target := filepath.Join(dir, filepath.Clean(e))
		if err := os.RemoveAll(target); err != nil {
			return fail(err)
		}
		src := filepath.Join(workspace, filepath.Clean(e))
		if _, err := os.Stat(src); err != nil {
			return fail(fmt.Errorf("workspace is missing editable path %s", e))
		}
		if err := copyTree(src, target); err != nil {
			return fail(err)
		}
	}
	if err := pointModule(filepath.Join(dir, ModFile), seedfallSrc); err != nil {
		return fail(err)
	}
	return dir, cleanup, nil
}

func pointModule(path, seedfallSrc string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	replace := fmt.Sprintf("\nreplace %s => %s\n", seedfallModule, filepath.ToSlash(seedfallSrc))
	return os.WriteFile(path, append(b, replace...), 0o644)
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst)
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(path, target)
		}
		return nil
	})
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
