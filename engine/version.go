package engine

import (
	"cmp"
	"os/exec"
	"path"
	"reflect"
	"runtime/debug"
	"strings"
)

const revisionLength = 12

func Version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	module := path.Dir(reflect.TypeFor[Runner]().PkgPath())
	if bi.Main.Path != module {
		for _, d := range bi.Deps {
			if d.Path == module {
				return d.Version
			}
		}
		return "unknown"
	}
	settings := map[string]string{}
	for _, s := range bi.Settings {
		settings[s.Key] = s.Value
	}
	version := bi.Main.Version
	if rev := settings["vcs.revision"]; rev != "" && (version == "" || version == "(devel)") {
		version = rev[:min(len(rev), revisionLength)]
		if settings["vcs.modified"] == "true" {
			version += "-dirty"
		}
	}
	return cmp.Or(version, "unknown")
}

func Commit(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
