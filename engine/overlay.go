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

const (
	acceptAnchor      = "func (fd *netFD) accept() (netfd *netFD, err error) {\n"
	readBufferAnchor  = "func setReadBuffer(fd *netFD, bytes int) error {\n"
	writeBufferAnchor = "func setWriteBuffer(fd *netFD, bytes int) error {\n"
	ignoreBufferSize  = "\tif fd.fakeNetFD == nil {\n\t\treturn nil\n\t}\n"
)

var netPatches = []patch{
	{file: "net_fake.go", anchor: "\nfunc socket(", replacement: "\nfunc fakeSocket("},
	{file: "fd_fake.go", anchor: acceptAnchor, replacement: acceptAnchor +
		"\tif lid, ok := seedfallListeners.Load(fd); ok {\n\t\treturn seedfallAccept(fd, lid.(int32))\n\t}\n"},
	{file: "sockopt_fake.go", anchor: readBufferAnchor, replacement: readBufferAnchor + ignoreBufferSize},
	{file: "sockopt_fake.go", anchor: writeBufferAnchor, replacement: writeBufferAnchor + ignoreBufferSize},
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
	patched := map[string]string{}
	for _, p := range netPatches {
		text, ok := patched[p.file]
		if !ok {
			b, err := os.ReadFile(filepath.Join(netDir, p.file))
			if err != nil {
				return "", fmt.Errorf("net overlay: %w", err)
			}
			text = string(b)
		}
		if patched[p.file], err = applyPatch(text, p); err != nil {
			return "", err
		}
	}
	replace := map[string]string{}
	for _, file := range sortedKeys(patched) {
		if replace[filepath.Join(netDir, file)], err = writeFile(filepath.Join(out, file), patched[file]); err != nil {
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
	var flags []string
	if cfg.CompatOverlay {
		modfile, err := writeCompatModule(cfg)
		if err != nil {
			return nil, err
		}
		if modfile != "" {
			flags = append(flags, "-modfile", modfile)
		}
	}
	if cfg.NetOverlay {
		overlay, err := writeNetOverlay(cfg.Dir)
		if err != nil {
			return nil, err
		}
		flags = append(flags, "-overlay", overlay)
	}
	return flags, nil
}
