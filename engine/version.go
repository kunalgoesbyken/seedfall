package engine

import (
	"cmp"
	"os/exec"
	"path"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
)

const revisionLength = 12

var buildVersion, buildCommit, buildDate string

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

func Binary() BuildInfo {
	info := BuildInfo{Version: Version(), Commit: buildCommit, Date: buildDate, GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		settings := vcsSettings(bi)
		info.Commit, info.Date = cmp.Or(info.Commit, settings["vcs.revision"]), cmp.Or(info.Date, settings["vcs.time"])
	}
	return info
}

func vcsSettings(bi *debug.BuildInfo) map[string]string {
	settings := map[string]string{}
	for _, s := range bi.Settings {
		settings[s.Key] = s.Value
	}
	return settings
}

func Version() string {
	if buildVersion != "" {
		return buildVersion
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	module := path.Dir(reflect.TypeFor[Runner]().PkgPath())
	if bi.Main.Path != module {
		for _, d := range bi.Deps {
			if d.Path == module {
				return cmp.Or(d.Version, "unknown")
			}
		}
		return "unknown"
	}
	settings := vcsSettings(bi)
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
