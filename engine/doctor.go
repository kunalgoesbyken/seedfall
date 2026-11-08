package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	probeModule  = "module seedfallprobe\n"
	probeEmpty   = "package main\n\nfunc main() {}\n"
	probeNetDial = "package main\n\nimport \"net\"\n\nvar _ = net.Dial\n\nfunc main() {}\n"
)

func GoEnv(key string) (string, error) { return goEnv(".", key) }

func ProbeWasip1Build() error { return probeBuild(probeEmpty, false) }

func ProbeNetOverlay() error { return probeBuild(probeNetDial, true) }

func probeBuild(source string, netOverlay bool) error {
	dir, err := os.MkdirTemp("", "seedfall-doctor-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for name, content := range map[string]string{"go.mod": probeModule, "main.go": source} {
		if _, err := writeFile(filepath.Join(dir, name), content); err != nil {
			return err
		}
	}
	args := []string{"build"}
	if netOverlay {
		replace := map[string]string{}
		if err := writeNetOverlay(dir, replace); err != nil {
			return err
		}
		overlay, err := writeOverlayFile(dir, replace)
		if err != nil {
			return err
		}
		args = append(args, "-overlay", overlay)
	}
	cmd := exec.Command("go", append(args, "-o", filepath.Join(dir, "probe.wasm"), ".")...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
