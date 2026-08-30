package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostWrappersFixture sets up a temp HOME with a user config and (optionally) generated
// wrappers, then returns Options wired to a controllable PATH.
func hostWrappersFixture(t *testing.T, optIn bool, wrappers []string, pathEnv string) (*Options, *reporter, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"host_wrappers": false}`
	if optIn {
		body = `{"host_wrappers": true}`
	}
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if wrappers != nil {
		dir := filepath.Join(home, ".local", "share", "yolo-jail", "bin", "wrap")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, w := range wrappers {
			if err := os.WriteFile(filepath.Join(dir, w), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	var buf bytes.Buffer
	o := &Options{Getenv: func(k string) string {
		switch k {
		case "PATH":
			return pathEnv
		case "YOLO_VERSION":
			return "" // host
		}
		return ""
	}}
	return o, newReporter(&buf, false), &buf
}

func wrapDirIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "bin", "wrap")
}

// TestHostWrappersSilentWhenNotOptedIn: not opted in means no row at all. This is what
// keeps the feature from being a nag for everyone who never asked for it.
func TestHostWrappersSilentWhenNotOptedIn(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, false, nil, "/bin")
	o.sectionHostWrappers(r)
	if buf.Len() != 0 {
		t.Errorf("printed something without the opt-in:\n%s", buf.String())
	}
	if r.warned != 0 || r.passed != 0 {
		t.Errorf("counted a row without the opt-in: warned=%d passed=%d", r.warned, r.passed)
	}
}

// TestHostWrappersSilentInJail: the key is host-only, and a jail has neither a user shell
// nor the host's PATH.
func TestHostWrappersSilentInJail(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude"}, "/bin")
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9"
		}
		return "/bin"
	}
	o.sectionHostWrappers(r)
	if buf.Len() != 0 {
		t.Errorf("printed something in-jail:\n%s", buf.String())
	}
}

// TestHostWrappersWarnsWhenNotOnPath is the row this section exists for: generated
// wrappers that nothing on PATH can reach are inert configuration, and it must be in the
// summary-COUNTED channel so it cannot be scrolled past.
func TestHostWrappersWarnsWhenNotOnPath(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude", "pi"}, "/bin:/usr/bin")
	o.sectionHostWrappers(r)
	out := buf.String()
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1 (it must be summary-counted)", r.warned)
	}
	if !strings.Contains(out, "[WARN]") {
		t.Errorf("no WARN badge:\n%s", out)
	}
	if !strings.Contains(out, "NOT on PATH") {
		t.Errorf("output does not say the dir is not on PATH:\n%s", out)
	}
	// The remedy must be present and must PREPEND — appending puts the wrapper behind
	// the real binary and it never runs.
	if !strings.Contains(out, `:$PATH"`) {
		t.Errorf("the remedy does not prepend:\n%s", out)
	}
	// And it must say the absolute-path escape hatch still works, which is the whole
	// reason wrappers are generated unconditionally.
	if !strings.Contains(out, "absolute path") {
		t.Errorf("output does not mention the absolute-path escape hatch:\n%s", out)
	}
}

func TestHostWrappersPassesWhenOnPath(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{"claude"}, "")
	dir := wrapDirIn(t)
	o.Getenv = func(k string) string {
		if k == "PATH" {
			return "/bin:" + dir
		}
		return ""
	}
	o.sectionHostWrappers(r)
	if r.passed != 1 || r.warned != 0 {
		t.Errorf("passed=%d warned=%d, want 1/0:\n%s", r.passed, r.warned, buf.String())
	}
	if !strings.Contains(buf.String(), "[PASS]") {
		t.Errorf("no PASS badge:\n%s", buf.String())
	}
}

// TestHostWrappersWarnsWhenOptedInButNothingGenerated catches the "I set the key and
// never ran apply" state, which would otherwise look identical to working.
func TestHostWrappersWarnsWhenOptedInButNothingGenerated(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, nil, "/bin")
	o.sectionHostWrappers(r)
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1:\n%s", r.warned, buf.String())
	}
	if !strings.Contains(buf.String(), "yolo host apply") {
		t.Errorf("the remedy does not name the command:\n%s", buf.String())
	}
}

func TestHostWrappersWarnsOnEmptyDir(t *testing.T) {
	o, r, buf := hostWrappersFixture(t, true, []string{}, "/bin")
	o.sectionHostWrappers(r)
	if r.warned != 1 {
		t.Errorf("warned = %d, want 1:\n%s", r.warned, buf.String())
	}
}
