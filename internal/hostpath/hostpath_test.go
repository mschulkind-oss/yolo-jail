package hostpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostpath_test.go pins the launch PATH's construction (docs/design/host-launch-environment.md
// §2.2, HE-D3, HE-D4), its lookup (HE-D5) and the miss line (HE-D2) against a fake PATH and a
// temp home. The call sites that read it are pinned in internal/cli and internal/cli/check.

const sep = string(os.PathListSeparator)

// fakeHome is a temp HOME with the user config at its usual place, and no jail.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	return home
}

func writeExe(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTheLaunchPathIsTheAmbientPathThenHostPathsNewFolders: HE-D3's order — the PATH yolo was
// started with, then each host_path folder not already on it, in written order — with empty
// entries dropped and duplicates keeping their first occurrence. Each folder names its source.
func TestTheLaunchPathIsTheAmbientPathThenHostPathsNewFolders(t *testing.T) {
	l := New(strings.Join([]string{"/usr/bin", "", "/bin", "/usr/bin/"}, sep),
		[]string{"/opt/b", "/bin", "/opt/a", "/opt/b"}, nil, "/home/u")
	if got, want := l.Value(), strings.Join([]string{"/usr/bin", "/bin", "/opt/b", "/opt/a"}, sep); got != want {
		t.Errorf("Value = %q, want %q", got, want)
	}
	if got := strings.Join(l.Added(), sep); got != "/opt/b"+sep+"/opt/a" {
		t.Errorf("Added = %q: a host_path folder already on the PATH yolo was started with is not added again", got)
	}
	if got := strings.Join(l.Declared(), sep); got != strings.Join([]string{"/opt/b", "/bin", "/opt/a", "/opt/b"}, sep) {
		t.Errorf("Declared = %q, want host_path as written", got)
	}
	for dir, want := range map[string]string{"/usr/bin": "the PATH yolo was started with",
		"/bin": "the PATH yolo was started with", "/opt/a": "host_path", "/nowhere": ""} {
		if got := l.Source(dir); got != want {
			t.Errorf("Source(%s) = %q, want %q", dir, got, want)
		}
	}
	// No host_path: the launch PATH is the PATH yolo was started with, which is what every check
	// read before the key existed.
	if got := New("/usr/bin"+sep+"/bin", nil, nil, "/home/u").Value(); got != "/usr/bin"+sep+"/bin" {
		t.Errorf("with no host_path, Value = %q, want the ambient PATH", got)
	}
}

// TestWithNoPathTheChecksSearchHostPathAlone: HE-D4 — started with no PATH, or an empty one, the
// launch PATH is host_path's folders alone, and the miss line says yolo was started with no PATH.
// WithStandIn is the child's variant (HP-D12): the stand-in folders ahead of host_path, named as
// such; with a PATH it changes nothing.
func TestWithNoPathTheChecksSearchHostPathAlone(t *testing.T) {
	fakeHome(t)
	for _, empty := range []string{"", sep, sep + sep} {
		l := New(empty, []string{"/opt/tools"}, nil, "/home/u")
		if l.Started() || l.Value() != "/opt/tools" {
			t.Errorf("PATH %q: started=%v value=%q, want not started and host_path alone", empty, l.Started(), l.Value())
		}
		if line := l.MissLine(Miss{Bin: "rg", Launch: true}); !strings.Contains(line,
			"is not on this launch's PATH, /opt/tools, host_path's folders alone: yolo was started with no PATH") {
			t.Errorf("miss line = %q", line)
		}
		child := l.WithStandIn([]string{"/usr/bin"})
		if got := child.Value(); got != "/usr/bin"+sep+"/opt/tools" {
			t.Errorf("the child's launch PATH = %q, want the stand-in then host_path's new folders", got)
		}
		if got := child.Source("/usr/bin"); !strings.Contains(got, "started with no PATH") {
			t.Errorf("the stand-in's source = %q", got)
		}
		if line := child.MissLine(Miss{Bin: "rg", Launch: true}); !strings.Contains(line,
			"/usr/bin, the system folders yolo uses when it is started with no PATH, then host_path's /opt/tools.") {
			t.Errorf("the child's miss line = %q", line)
		}
	}
	if line := New("", nil, nil, "/home/u").MissLine(Miss{Bin: "rg"}); !strings.Contains(line,
		"which is empty: yolo was started with no PATH, and host_path names no folder") {
		t.Errorf("no PATH and no host_path: %q", line)
	}
	withPath := New("/usr/bin", []string{"/opt/tools"}, nil, "/home/u")
	if withPath.WithStandIn([]string{"/sbin"}) != withPath {
		t.Error("WithStandIn changed a launch PATH yolo was started with")
	}
}

// TestLookPathFindsAHostPathFolderAndSkipsYolosOwn: a program only in a host_path folder resolves,
// the PATH yolo was started with wins over host_path for a name both hold, and a wrapper in a
// skipped folder never reads as the program it wraps (HE-D5).
func TestLookPathFindsAHostPathFolderAndSkipsYolosOwn(t *testing.T) {
	fakeHome(t)
	root := t.TempDir()
	ambient, extra, wrap := filepath.Join(root, "ambient"), filepath.Join(root, "extra"), filepath.Join(root, "yolo", "bin", "wrap")
	both := writeExe(t, ambient, "both")
	writeExe(t, extra, "both")
	only := writeExe(t, extra, "only")
	writeExe(t, wrap, "wrapped")
	l := New(wrap+sep+ambient, []string{extra}, []string{filepath.Join(root, "yolo", "bin")}, "/home/u")
	if got, err := l.LookPath("both"); err != nil || got != both {
		t.Errorf("LookPath(both) = %q, %v; want the ambient copy %s", got, err, both)
	}
	if got, err := l.LookPath("only"); err != nil || got != only {
		t.Errorf("LookPath(only) = %q, %v; want the host_path copy %s", got, err, only)
	}
	if got, err := l.LookPath("wrapped"); err == nil {
		t.Errorf("LookPath(wrapped) = %q: a wrapper in yolo's own folder read as the program", got)
	}
	if line := l.MissLine(Miss{Bin: "wrapped"}); !strings.Contains(line, "(skipping yolo's own "+wrap+")") {
		t.Errorf("the miss line does not name the skipped folder: %q", line)
	}
	if got, err := l.LookPathSkipping("both", ambient); err != nil || got != filepath.Join(extra, "both") {
		t.Errorf("LookPathSkipping(both, ambient) = %q, %v; want the host_path copy", got, err)
	}
}

// TestTheMissLineNamesTheProgramThePackThePathAndTheFix: the example line of §4.2, word for word
// from a bare /usr/bin:/bin with rg only in ~/.cargo/bin — which never makes the lookup pass — and
// the variants: host_path's part marked, no hint clause when no hint folder holds the program, a
// program of a pack, several packs, the checking verbs' wording, and nothing in a jail.
func TestTheMissLineNamesTheProgramThePackThePathAndTheFix(t *testing.T) {
	home := fakeHome(t)
	writeExe(t, filepath.Join(home, ".cargo", "bin"), "rg")
	writeExe(t, filepath.Join(home, ".cargo", "bin"), "yolo-hint-only-tool")
	if _, err := New(t.TempDir(), nil, nil, home).LookPath("yolo-hint-only-tool"); err == nil {
		t.Fatal("a hint folder made the lookup pass")
	}
	bare := New("/usr/bin"+sep+"/bin", nil, nil, home)
	want := `rg (required by the guardrails pack) is not on this launch's PATH, /usr/bin:/bin, the PATH ` +
		`yolo was started with. If rg is installed, add its folder to "host_path" in ` +
		`~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.`
	if got := bare.MissLine(Miss{Bin: "rg", Requires: []string{"guardrails"}, Launch: true}); got != want {
		t.Errorf("miss line =\n  %s\nwant\n  %s", got, want)
	}

	marked := New("/usr/bin"+sep+"/bin", []string{filepath.Join(home, ".local", "bin")}, nil, home)
	got := marked.MissLine(Miss{Bin: "fd", Programs: []string{"a", "b"}})
	for _, part := range []string{"fd (a program of the a and b packs) is not on the PATH yolo searched, ",
		"/usr/bin:/bin, the PATH yolo was started with, then host_path's ~/.local/bin.",
		`add its folder to "host_path"`} {
		if !strings.Contains(got, part) {
			t.Errorf("miss line lacks %q:\n%s", part, got)
		}
	}
	if strings.Contains(got, "has one") {
		t.Errorf("a hint clause with no hint folder holding the program:\n%s", got)
	}
	if got := bare.MissLine(Miss{Bin: "x", Requires: []string{"a", "b", "c"}}); !strings.Contains(got,
		"x (required by the a, b and c packs)") {
		t.Errorf("three packs: %s", got)
	}
	if got := bare.MissLine(Miss{Bin: "notes-sync", Launch: true}); !strings.HasPrefix(got,
		"notes-sync is not on this launch's PATH") {
		t.Errorf("a program no pack names: %s", got)
	}
	jail := New("/usr/bin", nil, nil, home)
	jail.jail = true
	if got := jail.MissLine(Miss{Bin: "rg"}); got != "" {
		t.Errorf("in a jail the miss line names a host key: %q", got)
	}
}

// TestHintIsAHintNeverAFolderOnThePath: a hint folder already on the launch PATH is never named
// (a miss there is not a missing folder), a non-executable file is no hint, and the hint list is
// read in its compiled order.
func TestHintIsAHintNeverAFolderOnThePath(t *testing.T) {
	home := fakeHome(t)
	cargo := filepath.Join(home, ".cargo", "bin")
	writeExe(t, cargo, "tool")
	if err := os.WriteFile(filepath.Join(cargo, "plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := New("/usr/bin", nil, nil, home).Hint("tool"); got != "~/.cargo/bin" {
		t.Errorf("Hint(tool) = %q, want ~/.cargo/bin", got)
	}
	if got := New(cargo, nil, nil, home).Hint("tool"); got != "" {
		t.Errorf("Hint named a folder already on the PATH: %q", got)
	}
	if got := New("/usr/bin", nil, nil, home).Hint("plain"); got != "" {
		t.Errorf("Hint named a folder whose file is not executable: %q", got)
	}
	writeExe(t, filepath.Join(home, ".local", "bin"), "tool")
	if got := New("/usr/bin", nil, nil, home).Hint("tool"); got != "~/.local/bin" {
		t.Errorf("Hint(tool) = %q, want ~/.local/bin, which the compiled list names before ~/.cargo/bin", got)
	}
}

// TestResolveReadsTheUserConfigsHostPathAndPassesThroughInAJail: the production resolver takes
// host_path from the user config and skips yolo's generated tree; in a jail it returns the process
// PATH unchanged, ignores host_path, and prints no miss line (§3, "In-jail").
func TestResolveReadsTheUserConfigsHostPathAndPassesThroughInAJail(t *testing.T) {
	home := fakeHome(t)
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(`{"host_path": ["~/tools/bin", "/opt/x/bin"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wrap := filepath.Join(home, ".local", "share", "yolo-jail", "bin", "wrap")
	writeExe(t, wrap, "rg")
	l := Resolve(wrap + sep + "/usr/bin")
	if want := strings.Join([]string{wrap, "/usr/bin", filepath.Join(home, "tools", "bin"), "/opt/x/bin"}, sep); l.Value() != want {
		t.Errorf("Resolve = %q, want %q", l.Value(), want)
	}
	if got, err := l.LookPath("rg"); err == nil {
		t.Errorf("the production resolver read the wrapper %s as rg", got)
	}
	t.Setenv("YOLO_VERSION", "0.0.0-test")
	jail := Resolve("/jail/bin" + sep + "/usr/bin")
	if jail.Value() != "/jail/bin"+sep+"/usr/bin" || len(jail.Added()) != 0 || !jail.InJail() {
		t.Errorf("in a jail Resolve = %q (added %q), want the process PATH unchanged", jail.Value(), jail.Added())
	}
	if line := jail.MissLine(Miss{Bin: "rg"}); line != "" {
		t.Errorf("in a jail a miss line: %q", line)
	}
}
