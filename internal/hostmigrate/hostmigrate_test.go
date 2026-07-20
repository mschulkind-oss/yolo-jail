package hostmigrate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeVenv builds a Python virtualenv at root holding the named console
// scripts, and returns the venv's bin directory. Mirrors the layout uv tool
// install produces: <root>/pyvenv.cfg plus <root>/bin/<script>.
func fakeVenv(t *testing.T, root string, scripts ...string) string {
	t.Helper()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pyvenv.cfg"), []byte("version = 3.13.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, s := range scripts {
		p := filepath.Join(binDir, s)
		if err := os.WriteFile(p, []byte("#!/usr/bin/python3\nimport yolo_jail\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return binDir
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// testMigrator returns a Migrator over gobin with no uv present and nothing
// recognised as a Go binary unless the caller says otherwise.
func testMigrator(gobin string) (*Migrator, *bytes.Buffer) {
	var log bytes.Buffer
	return &Migrator{
		GOBIN:      gobin,
		IsGoBinary: func(string) bool { return false },
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
		Exec:       func(string, ...string) ([]byte, error) { return nil, errors.New("no exec") },
		Out:        &log,
	}, &log
}

// TestClassifyUvVenvSymlink pins the regression: a `yolo` in GOBIN that is a
// symlink into a uv tool venv. This is what `uv tool install yolo-jail` left
// behind, and what made `go install ./cmd/yolo` fail with "build output …
// already exists and is not an object file".
func TestClassifyUvVenvSymlink(t *testing.T) {
	dir := t.TempDir()
	gobin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	venvBin := fakeVenv(t, filepath.Join(dir, "uv", "tools", "yolo-jail"), "yolo")
	symlink(t, filepath.Join(venvBin, "yolo"), filepath.Join(gobin, "yolo"))

	m, _ := testMigrator(gobin)
	if got := m.classify(filepath.Join(gobin, "yolo")); got != KindPythonVenvLink {
		t.Fatalf("classify = %v, want KindPythonVenvLink", got)
	}
	if !KindPythonVenvLink.Stale() {
		t.Error("a python venv symlink must be considered stale")
	}
}

func TestClassifyKinds(t *testing.T) {
	dir := t.TempDir()
	gobin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}

	// Broken symlink — target never existed.
	symlink(t, filepath.Join(dir, "gone", "yolo"), filepath.Join(gobin, "broken"))
	// Python script copied in place rather than symlinked.
	write(t, filepath.Join(gobin, "pyscript"), "#!/usr/bin/env python3\nprint('hi')\n")
	// A shell script — not python, not Go: unidentifiable.
	write(t, filepath.Join(gobin, "shscript"), "#!/bin/sh\necho hi\n")
	// A symlink to a plain file outside any venv.
	write(t, filepath.Join(dir, "elsewhere"), "binary-ish")
	symlink(t, filepath.Join(dir, "elsewhere"), filepath.Join(gobin, "otherlink"))

	m, _ := testMigrator(gobin)
	cases := []struct {
		name string
		want Kind
	}{
		{"broken", KindBrokenLink},
		{"pyscript", KindPythonScript},
		{"shscript", KindUnknown},
		{"otherlink", KindUnknown},
		{"absent", KindAbsent},
	}
	for _, tc := range cases {
		if got := m.classify(filepath.Join(gobin, tc.name)); got != tc.want {
			t.Errorf("classify(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestClassifyGoBinaryIsNotStale guards the most dangerous mistake this
// package could make: deleting a perfectly good Go binary.
func TestClassifyGoBinaryIsNotStale(t *testing.T) {
	gobin := t.TempDir()
	write(t, filepath.Join(gobin, "yolo"), "\x7fELF-pretend-go-binary")

	m, _ := testMigrator(gobin)
	m.IsGoBinary = func(string) bool { return true }

	if got := m.classify(filepath.Join(gobin, "yolo")); got != KindGoBinary {
		t.Fatalf("classify = %v, want KindGoBinary", got)
	}
	if KindGoBinary.Stale() {
		t.Fatal("a Go binary must never be considered stale")
	}

	res, err := m.Preflight()
	if err != nil {
		t.Fatalf("Preflight over a Go binary: %v", err)
	}
	if !res.Clean() {
		t.Errorf("Preflight should be a no-op over a Go binary, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(gobin, "yolo")); err != nil {
		t.Error("Preflight deleted a Go binary")
	}
}

// TestPreflightRetiresAllConsoleScripts covers the end-to-end migration from
// a real-world Python install: all four console scripts symlinked into one
// uv tool venv.
func TestPreflightRetiresAllConsoleScripts(t *testing.T) {
	dir := t.TempDir()
	gobin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	venvBin := fakeVenv(t, filepath.Join(dir, "uv", "tools", "yolo-jail"), LegacyNames...)
	for _, n := range LegacyNames {
		symlink(t, filepath.Join(venvBin, n), filepath.Join(gobin, n))
	}
	// An unrelated neighbour that must survive untouched.
	write(t, filepath.Join(gobin, "ripgrep"), "#!/usr/bin/env python3\n")

	m, log := testMigrator(gobin)
	res, err := m.Preflight()
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(res.Removed) != len(LegacyNames) {
		t.Fatalf("removed %d entries, want %d: %v", len(res.Removed), len(LegacyNames), res.Removed)
	}
	for _, n := range LegacyNames {
		if _, err := os.Lstat(filepath.Join(gobin, n)); !os.IsNotExist(err) {
			t.Errorf("%s survived Preflight", n)
		}
	}
	if _, err := os.Stat(filepath.Join(gobin, "ripgrep")); err != nil {
		t.Error("Preflight removed an unrelated file")
	}
	if !strings.Contains(log.String(), "retired legacy") {
		t.Errorf("expected progress output, got %q", log.String())
	}
}

// TestPreflightRefusesUnknownBlocker is the safety valve: something at
// GOBIN/yolo that would break `go install` but that we cannot identify must
// be reported, not deleted.
func TestPreflightRefusesUnknownBlocker(t *testing.T) {
	gobin := t.TempDir()
	blocker := filepath.Join(gobin, "yolo")
	write(t, blocker, "#!/bin/sh\n# someone's hand-rolled wrapper\n")

	m, _ := testMigrator(gobin)
	res, err := m.Preflight()
	if err == nil {
		t.Fatal("expected an error for an unidentifiable blocker")
	}
	if res.Blocked != blocker {
		t.Errorf("Blocked = %q, want %q", res.Blocked, blocker)
	}
	if _, statErr := os.Stat(blocker); statErr != nil {
		t.Error("Preflight deleted a file it could not identify")
	}
	if !strings.Contains(err.Error(), blocker) {
		t.Errorf("error should name the offending path, got: %v", err)
	}
}

// TestPreflightCleanHostIsSilent — the common case, run on every install.
func TestPreflightCleanHostIsSilent(t *testing.T) {
	gobin := t.TempDir()
	m, log := testMigrator(gobin)

	res, err := m.Preflight()
	if err != nil {
		t.Fatalf("Preflight on a clean host: %v", err)
	}
	if !res.Clean() {
		t.Errorf("expected a clean result, got %+v", res)
	}
	if log.String() != "" {
		t.Errorf("expected no output on a clean host, got %q", log.String())
	}
}

// TestPreflightIsIdempotent — `just deploy` is documented as safe to re-run.
func TestPreflightIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	gobin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	venvBin := fakeVenv(t, filepath.Join(dir, "venv"), "yolo")
	symlink(t, filepath.Join(venvBin, "yolo"), filepath.Join(gobin, "yolo"))

	m, _ := testMigrator(gobin)
	if _, err := m.Preflight(); err != nil {
		t.Fatalf("first Preflight: %v", err)
	}
	res, err := m.Preflight()
	if err != nil {
		t.Fatalf("second Preflight: %v", err)
	}
	if !res.Clean() {
		t.Errorf("second pass should be a no-op, got %+v", res)
	}
}

// TestPreflightUninstallsUvTool checks we retire the distribution at its
// source, not just its symlinks — otherwise `uv tool upgrade` resurrects them.
func TestPreflightUninstallsUvTool(t *testing.T) {
	gobin := t.TempDir()
	m, log := testMigrator(gobin)
	m.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	var ran [][]string
	m.Exec = func(name string, args ...string) ([]byte, error) {
		ran = append(ran, append([]string{name}, args...))
		if len(args) > 1 && args[1] == "list" {
			return []byte("litellm v1.82.2\n- litellm\nyolo-jail v0.6.1.dev231+g1f504e7c2\n- yolo\n- yolo-ps\n"), nil
		}
		return nil, nil
	}

	res, err := m.Preflight()
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !res.UvUninstalled {
		t.Error("expected the uv tool to be uninstalled")
	}
	if len(ran) != 2 || strings.Join(ran[1], " ") != "uv tool uninstall yolo-jail" {
		t.Errorf("unexpected commands: %v", ran)
	}
	if !strings.Contains(log.String(), "retired legacy Python install") {
		t.Errorf("expected progress output, got %q", log.String())
	}
}

// TestPreflightSkipsUvWhenToolAbsent — a host with uv but no yolo-jail tool
// (or no uv at all) must not have `uv tool uninstall` run against it.
func TestPreflightSkipsUvWhenToolAbsent(t *testing.T) {
	gobin := t.TempDir()
	m, _ := testMigrator(gobin)
	m.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	var ran [][]string
	m.Exec = func(name string, args ...string) ([]byte, error) {
		ran = append(ran, append([]string{name}, args...))
		return []byte("litellm v1.82.2\n- litellm\n"), nil
	}

	res, err := m.Preflight()
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res.UvUninstalled {
		t.Error("uninstalled a tool that was not installed")
	}
	if len(ran) != 1 {
		t.Errorf("expected only `uv tool list`, got %v", ran)
	}
}

func TestUvToolListed(t *testing.T) {
	const out = "litellm v1.82.2\n- litellm\n- litellm-proxy\nyolo-jail v0.6.1.dev231+g1f504e7c2\n- yolo\n- yolo-ps\n"
	if !uvToolListed(out, "yolo-jail") {
		t.Error("yolo-jail should be detected as installed")
	}
	if uvToolListed(out, "yolo") {
		t.Error("`yolo` is a console script, not a tool name — must not match")
	}
	if uvToolListed(out, "litellm-proxy") {
		t.Error("`litellm-proxy` is a console script, not a tool name — must not match")
	}
	if uvToolListed("", "yolo-jail") {
		t.Error("empty output must not match")
	}
}

func TestInPythonVenv(t *testing.T) {
	dir := t.TempDir()
	venvBin := fakeVenv(t, filepath.Join(dir, "tool"), "yolo")
	if !inPythonVenv(filepath.Join(venvBin, "yolo")) {
		t.Error("executable inside a venv bin/ should be detected")
	}

	// A bin/ directory with no pyvenv.cfg beside it is not a venv.
	plain := filepath.Join(dir, "plain", "bin")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(plain, "yolo"), "x")
	if inPythonVenv(filepath.Join(plain, "yolo")) {
		t.Error("a plain bin/ dir must not be mistaken for a venv")
	}
}

func TestHasPythonShebang(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, content string
		want          bool
	}{
		{"uv-console-script", "#!/home/u/.local/share/uv/tools/yolo-jail/bin/python\nimport x\n", true},
		{"env-python", "#!/usr/bin/env python3\n", true},
		{"shell", "#!/bin/sh\necho hi\n", false},
		{"no-shebang", "just text\n", false},
		{"empty", "", false},
		{"python-not-in-shebang", "#!/bin/sh\n# python\n", false},
	}
	for _, tc := range cases {
		p := filepath.Join(dir, tc.name)
		write(t, p, tc.content)
		if got := hasPythonShebang(p); got != tc.want {
			t.Errorf("hasPythonShebang(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
