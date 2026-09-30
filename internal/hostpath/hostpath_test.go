package hostpath

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
)

// hostpath_test.go pins the launch PATH's construction (docs/design/host-launch-environment.md
// §2.2, HE-D3, HE-D4), its lookup (HE-D5) and the miss line (HE-D2) against a fake PATH and a
// temp home. The call sites that read it are pinned in internal/cli and internal/cli/check.

const sep = string(os.PathListSeparator)

// host is one test's fake machine: home is a temp HOME with the user config at its usual place and
// no jail, and root is a temp folder standing in for "/" under every hint folder the home does not
// hold.
//
// THE HINT FOLDERS ARE THE MACHINE'S UNLESS A TEST REPLACES THEM. hostfloor.HintLocations names
// /opt/homebrew/bin and /home/linuxbrew/.linuxbrew/bin outright, and no fake HOME moves those, so a
// Launch from bare New stats the real ones. On a Mac with Homebrew's fd, the guardrails pack's
// replacement for find, a miss line that must name no hint gained "; /opt/homebrew/bin has one.",
// while the same test passed on every machine without it. host.New hands every Launch a hint list
// of folders this test made, in the compiled order.
type host struct{ home, root string }

// resolvedTempDir is t.TempDir with its symlinks resolved where it is minted: on macOS the temp dir
// is under /var, a link to /private/var.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(d); err == nil {
		d = resolved
	}
	return d
}

func fakeHost(t *testing.T) host {
	t.Helper()
	h := host{home: resolvedTempDir(t), root: resolvedTempDir(t)}
	t.Setenv("HOME", h.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h.home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	return h
}

// New is the package's New on this machine: its home, and hint folders this test made.
func (h host) New(ambient string, declared, skip []string) *Launch {
	l := New(ambient, declared, skip, h.home)
	l.hints = h.hints()
	return l
}

// hints is hostfloor.HintLocations(h.home) with each folder outside the home moved under h.root.
func (h host) hints() []string {
	var out []string
	for _, d := range hostfloor.HintLocations(h.home) {
		if !strings.HasPrefix(d, h.home+string(filepath.Separator)) {
			d = filepath.Join(h.root, d)
		}
		out = append(out, d)
	}
	return out
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
// such; with a PATH it changes nothing. So with no PATH the checks' PATH is not the one the launch's
// child searches, and a launch's line from the checks' view (the gate's) says "the PATH yolo
// searched" rather than calling host_path's folders alone "this launch's PATH"; the child's own line
// (the exec's) is this launch's.
func TestWithNoPathTheChecksSearchHostPathAlone(t *testing.T) {
	h := fakeHost(t)
	for _, empty := range []string{"", sep, sep + sep} {
		l := h.New(empty, []string{"/opt/tools"}, nil)
		if l.Started() || l.Value() != "/opt/tools" {
			t.Errorf("PATH %q: started=%v value=%q, want not started and host_path alone", empty, l.Started(), l.Value())
		}
		if line := l.MissLine(Miss{Bin: "rg", Launch: true}); !strings.Contains(line,
			"is not on the PATH yolo searched, /opt/tools, host_path's folders alone: yolo was started with no PATH") {
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
			"is not on this launch's PATH, /usr/bin, the system folders yolo uses when it is started with no "+
				"PATH, then host_path's /opt/tools.") {
			t.Errorf("the child's miss line = %q", line)
		}
	}
	if line := h.New("", nil, nil).MissLine(Miss{Bin: "rg"}); !strings.Contains(line,
		"which is empty: yolo was started with no PATH, and host_path names no folder") {
		t.Errorf("no PATH and no host_path: %q", line)
	}
	withPath := h.New("/usr/bin", []string{"/opt/tools"}, nil)
	if withPath.WithStandIn([]string{"/sbin"}) != withPath {
		t.Error("WithStandIn changed a launch PATH yolo was started with")
	}
}

// TestLookPathFindsAHostPathFolderAndSkipsYolosOwn: a program only in a host_path folder resolves,
// the PATH yolo was started with wins over host_path for a name both hold, and a wrapper in a
// skipped folder never reads as the program it wraps (HE-D5).
func TestLookPathFindsAHostPathFolderAndSkipsYolosOwn(t *testing.T) {
	h := fakeHost(t)
	root := t.TempDir()
	ambient, extra, wrap := filepath.Join(root, "ambient"), filepath.Join(root, "extra"), filepath.Join(root, "yolo", "bin", "wrap")
	both := writeExe(t, ambient, "both")
	writeExe(t, extra, "both")
	only := writeExe(t, extra, "only")
	writeExe(t, wrap, "wrapped")
	l := h.New(wrap+sep+ambient, []string{extra}, []string{filepath.Join(root, "yolo", "bin")})
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
	h := fakeHost(t)
	home := h.home
	writeExe(t, filepath.Join(home, ".cargo", "bin"), "rg")
	writeExe(t, filepath.Join(home, ".cargo", "bin"), "yolo-hint-only-tool")
	if _, err := h.New(t.TempDir(), nil, nil).LookPath("yolo-hint-only-tool"); err == nil {
		t.Fatal("a hint folder made the lookup pass")
	}
	bare := h.New("/usr/bin"+sep+"/bin", nil, nil)
	want := `rg (required by the guardrails pack) is not on this launch's PATH, /usr/bin:/bin, the PATH ` +
		`yolo was started with. If rg is installed, add its folder to "host_path" in ` +
		`~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.`
	if got := bare.MissLine(Miss{Bin: "rg", Requires: []string{"guardrails"}, Launch: true}); got != want {
		t.Errorf("miss line =\n  %s\nwant\n  %s", got, want)
	}

	marked := h.New("/usr/bin"+sep+"/bin", []string{filepath.Join(home, ".local", "bin")}, nil)
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

	// The same line on a Mac with Homebrew's fd: the fixture's /opt/homebrew/bin holds one, and the
	// clause names that folder as written, since it is not under the home.
	brew := filepath.Join(h.root, "opt", "homebrew", "bin")
	if !slices.Contains(h.hints(), brew) {
		t.Fatalf("the fixture's hint folders %q do not stand in for /opt/homebrew/bin at %s", h.hints(), brew)
	}
	writeExe(t, brew, "fd")
	if got := marked.MissLine(Miss{Bin: "fd", Programs: []string{"a", "b"}}); !strings.HasSuffix(got,
		`add its folder to "host_path" in ~/.config/yolo-jail/config.jsonc; `+brew+" has one.") {
		t.Errorf("a hint folder outside the home holding the program is not named as written:\n%s", got)
	}
	if got := bare.MissLine(Miss{Bin: "x", Requires: []string{"a", "b", "c"}}); !strings.Contains(got,
		"x (required by the a, b and c packs)") {
		t.Errorf("three packs: %s", got)
	}
	if got := bare.MissLine(Miss{Bin: "notes-sync", Launch: true}); !strings.HasPrefix(got,
		"notes-sync is not on this launch's PATH") {
		t.Errorf("a program no pack names: %s", got)
	}
	jail := h.New("/usr/bin", nil, nil)
	jail.jail = true
	if got := jail.MissLine(Miss{Bin: "rg"}); got != "" {
		t.Errorf("in a jail the miss line names a host key: %q", got)
	}
}

// TestTheMissLineNamesEachRefusedHostPathEntry: a `host_path` entry the reader refused is named in
// the miss line with its reason and fix, between the PATH searched and the `host_path` fix, so the
// line never asks for a folder the user already listed without saying why that entry did not count.
//
// With `rg` and bare New this failed on every Mac with Homebrew's rg: the hint clause read the real
// /opt/homebrew/bin, and the line gained "; /opt/homebrew/bin has one." The fixture's hint folders
// (host) are what keep it off the machine; the program no machine has is a second guard.
func TestTheMissLineNamesEachRefusedHostPathEntry(t *testing.T) {
	l := fakeHost(t).New("/usr/bin", nil, nil)
	l.refused = []config.HostPathRefusal{
		{Entry: `"$HOME/.cargo/bin"`, Why: `host_path expands no variable, so write it "~/.cargo/bin"`},
		{Entry: `"~/x"`, Whole: true, Why: `host_path is a list of folders, so write it ["~/x"]`},
	}
	const bin = "yolo-test-no-such-tool"
	want := bin + ` is not on the PATH yolo searched, /usr/bin, the PATH yolo was started with. ` +
		`host_path's entry "$HOME/.cargo/bin" is ignored: host_path expands no variable, so write it "~/.cargo/bin". ` +
		`host_path's value "~/x" is ignored: host_path is a list of folders, so write it ["~/x"]. ` +
		`If ` + bin + ` is installed, add its folder to "host_path" in ~/.config/yolo-jail/config.jsonc.`
	if got := l.MissLine(Miss{Bin: bin}); got != want {
		t.Errorf("MissLine =\n  %s\nwant\n  %s", got, want)
	}
}

// TestHintIsAHintNeverAFolderOnThePath: a hint folder already on the launch PATH is never named
// (a miss there is not a missing folder), a non-executable file is no hint, and the hint list is
// read in its compiled order.
func TestHintIsAHintNeverAFolderOnThePath(t *testing.T) {
	h := fakeHost(t)
	cargo := filepath.Join(h.home, ".cargo", "bin")
	writeExe(t, cargo, "tool")
	if err := os.WriteFile(filepath.Join(cargo, "plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := h.New("/usr/bin", nil, nil).Hint("tool"); got != "~/.cargo/bin" {
		t.Errorf("Hint(tool) = %q, want ~/.cargo/bin", got)
	}
	if got := h.New(cargo, nil, nil).Hint("tool"); got != "" {
		t.Errorf("Hint named a folder already on the PATH: %q", got)
	}
	if got := h.New("/usr/bin", nil, nil).Hint("plain"); got != "" {
		t.Errorf("Hint named a folder whose file is not executable: %q", got)
	}
	writeExe(t, filepath.Join(h.home, ".local", "bin"), "tool")
	if got := h.New("/usr/bin", nil, nil).Hint("tool"); got != "~/.local/bin" {
		t.Errorf("Hint(tool) = %q, want ~/.local/bin, which the compiled list names before ~/.cargo/bin", got)
	}
}

// TestResolveReadsTheUserConfigsHostPathAndPassesThroughInAJail: the production resolver takes
// host_path from the user config and skips yolo's generated tree; in a jail it returns the process
// PATH unchanged, ignores host_path, and prints no miss line (§3, "In-jail").
func TestResolveReadsTheUserConfigsHostPathAndPassesThroughInAJail(t *testing.T) {
	home := fakeHost(t).home
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
	// The production hint folders are the compiled list for the home. Every test above replaces
	// them (host.New), so this is what fails if New stops setting them.
	if want := hostfloor.HintLocations(home); !slices.Equal(l.hints, want) {
		t.Errorf("Resolve's hint folders = %q, want the compiled list %q", l.hints, want)
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
