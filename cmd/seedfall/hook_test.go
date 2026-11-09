package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hookStdin(t *testing.T, input string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = saved
		f.Close()
	})
}

func TestHookDoesNothingWhenSeedsAreUnsetEvenWithEmptyInput(t *testing.T) {
	t.Setenv("SEEDFALL_HOOK_SEEDS", "")
	hookStdin(t, "")
	if err := cmdHook(nil); err != nil {
		t.Fatal(err)
	}
}

func TestHookRejectsUnreadableInputWhenSeedsAreSet(t *testing.T) {
	t.Setenv("SEEDFALL_HOOK_SEEDS", "1-2")
	hookStdin(t, "")
	if err := cmdHook(nil); err == nil {
		t.Fatal("empty input was accepted")
	}
}

func TestHookConfigPath(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "elsewhere", "seedfall.json")
	if got := hookConfig("work", abs); got != abs {
		t.Errorf("absolute config = %q, want %q", got, abs)
	}
	if got, want := hookConfig("work", "conf.json"), filepath.Join("work", "conf.json"); got != want {
		t.Errorf("relative config = %q, want %q", got, want)
	}
	if got, want := hookConfig("work", ""), filepath.Join("work", "seedfall.json"); got != want {
		t.Errorf("default config = %q, want %q", got, want)
	}
}

func TestHookSkipsAMissingConfigButReportsOtherErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SEEDFALL_HOOK_SEEDS", "1-2")
	t.Setenv("SEEDFALL_HOOK_CONFIG", filepath.Join(dir, "missing.json"))
	hookStdin(t, `{"cwd": "`+filepath.ToSlash(dir)+`"}`)
	if err := cmdHook(nil); err != nil {
		t.Fatalf("missing config: %v", err)
	}
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEEDFALL_HOOK_CONFIG", broken)
	hookStdin(t, `{"cwd": "elsewhere"}`)
	if err := cmdHook(nil); err == nil {
		t.Fatal("an absolute config was not used")
	}
	t.Setenv("SEEDFALL_HOOK_CONFIG", "")
	hookStdin(t, `{"cwd": "bad\u0000dir"}`)
	if err := cmdHook(nil); err == nil || strings.Contains(err.Error(), "not exist") {
		t.Fatalf("a stat error was swallowed: %v", err)
	}
}
