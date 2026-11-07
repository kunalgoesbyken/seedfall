package engine

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed overlay/net_seedfall.go.txt
var netSeedfall string

type patch struct {
	file, anchor, replacement string
}

const acceptAnchor = "func (fd *netFD) accept() (netfd *netFD, err error) {\n"

var netPatches = []patch{
	{file: "net_fake.go", anchor: "\nfunc socket(", replacement: "\nfunc fakeSocket("},
	{file: "fd_fake.go", anchor: acceptAnchor, replacement: acceptAnchor +
		"\tif lid, ok := seedfallListeners.Load(fd); ok {\n\t\treturn seedfallAccept(fd, lid.(int32))\n\t}\n"},
}

func applyPatch(src string, p patch) (string, error) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if n := strings.Count(src, p.anchor); n != 1 {
		return "", fmt.Errorf("net overlay: expected one %q in %s, found %d; this Go version is not supported yet", strings.TrimSpace(p.anchor), p.file, n)
	}
	return strings.Replace(src, p.anchor, p.replacement, 1), nil
}

func goEnv(dir, key string) (string, error) {
	cmd := exec.Command("go", "env", key)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func writeNetOverlay(dir string) (string, error) {
	goroot, err := goEnv(dir, "GOROOT")
	if err != nil {
		return "", err
	}
	netDir := filepath.Join(goroot, "src", "net")
	out, err := filepath.Abs(filepath.Join(dir, ".seedfall", "overlay"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	replace := map[string]string{}
	for _, p := range netPatches {
		src := filepath.Join(netDir, p.file)
		b, err := os.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("net overlay: %w", err)
		}
		patched, err := applyPatch(string(b), p)
		if err != nil {
			return "", err
		}
		if replace[src], err = writeFile(filepath.Join(out, p.file), patched); err != nil {
			return "", err
		}
	}
	added := filepath.Join(netDir, "net_seedfall_wasip1.go")
	if replace[added], err = writeFile(filepath.Join(out, "net_seedfall_wasip1.go"), netSeedfall); err != nil {
		return "", err
	}
	b, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		return "", err
	}
	return writeFile(filepath.Join(out, "overlay.json"), string(b))
}

func writeFile(path, content string) (string, error) {
	return path, os.WriteFile(path, []byte(content), 0o644)
}

func buildFlags(cfg *Config) ([]string, error) {
	if !cfg.NetOverlay {
		return nil, nil
	}
	overlay, err := writeNetOverlay(cfg.Dir)
	if err != nil {
		return nil, err
	}
	return []string{"-overlay", overlay}, nil
}
