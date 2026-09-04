package capture

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// relocate_test.go measures the RECORDING half of relocation — the half slice 6 owns.
//
// Every test drives capture.Run rather than scanContentRefs, so the CALL SITE is what is pinned:
// delete the `if opts.ScanContentRefs` block in Run and these go red, which a test on the scanner
// alone would not (AGENTS.md's pinned-callee shape). The scanner's one genuinely internal
// property — a reference straddling a read window — is the single direct test at the bottom,
// because there is no way to force a 64 KiB boundary from a fixture installer without writing a
// file whose only purpose is to be that boundary anyway.
//
// What is NOT measured here: the REWRITE. Materialize is slice 4's, and nothing in this package
// substitutes a prefix into anything. These tests pin what a rewrite will be handed.

// relocInstaller writes the four shapes that decide a capture's relocatability, plus one that
// must not be reported.
//
// $HOME is spliced in by the shell, so every absolute reference below is the real staging
// prefix the driver will look for — the same way a vendor installer bakes the home it ran under
// into the shim it writes (claude's ~/.local/bin/claude is an absolute symlink, MEASURED in this
// jail with `ls -l`).
const relocInstaller = `#!/bin/sh
set -eu
mkdir -p "$HOME/.local/share/vendor/1.0.0"
printf 'the binary\n' > "$HOME/.local/share/vendor/1.0.0/vendor"
chmod 755 "$HOME/.local/share/vendor/1.0.0/vendor"

# 1. An ABSOLUTE SYMLINK into the staging prefix.
mkdir -p "$HOME/.local/bin"
ln -s "$HOME/.local/share/vendor/1.0.0/vendor" "$HOME/.local/bin/vendor"

# 2. A TEXT file embedding the staging prefix — the launcher shim shape.
printf '#!/bin/sh\nexec %s/.local/share/vendor/1.0.0/vendor "$@"\n' "$HOME" \
  > "$HOME/.local/bin/vendor-shim"
chmod 755 "$HOME/.local/bin/vendor-shim"

# 3. A RELATIVE symlink, which is already correct anywhere and must NOT be reported.
ln -s ../share/vendor/1.0.0/vendor "$HOME/.local/bin/vendor-rel"

# 4. A file with NO reference at all.
printf 'no paths in here\n' > "$HOME/.local/share/vendor/1.0.0/README"
`

// relocBinaryInstaller is relocInstaller's non-relocatable twin: the reference lives in a file
// with a NUL byte in it, which no prefix substitution can safely rewrite.
const relocBinaryInstaller = `#!/bin/sh
set -eu
mkdir -p "$HOME/.local/share/vendor/1.0.0"
printf 'ELF\000\000\000%s/.local/share/vendor\000tail\n' "$HOME" \
  > "$HOME/.local/share/vendor/1.0.0/vendor"
chmod 755 "$HOME/.local/share/vendor/1.0.0/vendor"
`

func runRelocCapture(t *testing.T, script string, scan bool) *Manifest {
	t.Helper()
	home := t.TempDir()
	fixtureHome(t, home)
	var stderr bytes.Buffer
	res, err := Run(Options{
		Home:            home,
		Out:             filepath.Join(t.TempDir(), "staging"),
		Command:         writeInstaller(t, script),
		ScanContentRefs: scan,
		Stderr:          &stderr,
	})
	must(t, err)
	return res.Manifest
}

func refFor(m *Manifest, path, kind string) (AbsoluteRef, bool) {
	for _, r := range m.AbsoluteRefs {
		if r.Path == path && r.Kind == kind {
			return r, true
		}
	}
	return AbsoluteRef{}, false
}

// THE SLICE-6 PROPERTY: with the scan on, every absolute reference an installer embedded is in
// the manifest and the entry says it may be moved.
func TestCaptureRecordsFileContentReferencesAndIsRelocatable(t *testing.T) {
	m := runRelocCapture(t, relocInstaller, true)

	if m.RefScan != RefScanFull {
		t.Errorf("refScan = %q, want %q", m.RefScan, RefScanFull)
	}
	if !m.Relocatable {
		t.Errorf("a text-only install is not relocatable: %v", m.NotRelocatable)
	}
	if len(m.NotRelocatable) != 0 {
		t.Errorf("relocatable capture carries reasons it is not: %v", m.NotRelocatable)
	}

	// The symlink half, which the walk sees for free.
	link, ok := refFor(m, ".local/bin/vendor", RefSymlinkTarget)
	if !ok {
		t.Fatalf("absolute symlink not recorded: %+v", m.AbsoluteRefs)
	}
	if link.Value != m.Home+"/.local/share/vendor/1.0.0/vendor" {
		t.Errorf("symlink ref value = %q, want the target verbatim", link.Value)
	}

	// The content half, which is what this slice adds. The Value is the PREFIX a rewrite
	// substitutes, not the whole line it appeared in.
	shim, ok := refFor(m, ".local/bin/vendor-shim", RefFileContent)
	if !ok {
		t.Fatalf("the shim's embedded absolute path was not recorded: %+v", m.AbsoluteRefs)
	}
	if shim.Value != m.Home {
		t.Errorf("content ref value = %q, want the capture home %q", shim.Value, m.Home)
	}

	// A relative symlink is already correct anywhere; reporting it would hand a rewrite an
	// edit it must not make. Same for a file with no reference in it.
	for _, path := range []string{".local/bin/vendor-rel", ".local/share/vendor/1.0.0/README"} {
		for _, kind := range []string{RefSymlinkTarget, RefFileContent} {
			if _, ok := refFor(m, path, kind); ok {
				t.Errorf("%s reported as a %s reference; it names nothing absolute", path, kind)
			}
		}
	}

	// One ref per file, never per occurrence: a rewrite substitutes the prefix everywhere in
	// the file, so a second entry would be a second instruction to do the same thing.
	seen := map[string]int{}
	for _, r := range m.AbsoluteRefs {
		seen[r.Path+"|"+r.Kind]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("%s recorded %d times", k, n)
		}
	}
}

// THE DEFAULT, and the one that protects every container capture: without the scan the entry is
// NOT relocatable, and it says why. A symlink-only walk finding nothing is not evidence that
// nothing is there.
func TestCaptureWithoutTheScanIsNotRelocatable(t *testing.T) {
	m := runRelocCapture(t, relocInstaller, false)

	if m.RefScan != RefScanSymlinks {
		t.Errorf("refScan = %q, want %q", m.RefScan, RefScanSymlinks)
	}
	if m.Relocatable {
		t.Error("a capture whose file contents were never scanned claims to be relocatable")
	}
	if len(m.NotRelocatable) != 1 || !strings.Contains(m.NotRelocatable[0], RefScanSymlinks) {
		t.Errorf("the refusal does not name the scan that was not run: %v", m.NotRelocatable)
	}
	// The symlink half still runs — it costs nothing and a rewrite needs it.
	if _, ok := refFor(m, ".local/bin/vendor", RefSymlinkTarget); !ok {
		t.Error("the symlink reference was dropped along with the content scan")
	}
	// The content half did not.
	if _, ok := refFor(m, ".local/bin/vendor-shim", RefFileContent); ok {
		t.Error("a content reference was recorded without the scan being asked for")
	}
}

// A reference inside a binary is the fail-safe case: it is RECORDED (a rewrite that can pad or
// understand the format still gets told about it) and it makes the entry non-relocatable, with a
// reason naming the file rather than the decision.
func TestABinaryReferenceMakesTheCaptureNonRelocatable(t *testing.T) {
	m := runRelocCapture(t, relocBinaryInstaller, true)

	if m.RefScan != RefScanFull {
		t.Fatalf("refScan = %q", m.RefScan)
	}
	if _, ok := refFor(m, ".local/share/vendor/1.0.0/vendor", RefFileContent); !ok {
		t.Errorf("the binary's embedded path was not recorded: %+v", m.AbsoluteRefs)
	}
	if m.Relocatable {
		t.Error("a capture with a path embedded in a binary claims to be relocatable")
	}
	if len(m.NotRelocatable) != 1 ||
		!strings.Contains(m.NotRelocatable[0], ".local/share/vendor/1.0.0/vendor") {
		t.Errorf("the refusal does not name the offending file: %v", m.NotRelocatable)
	}
}

// The manifest round-trips: what a materialize reads back is what the driver decided.
func TestRelocationFieldsSurviveTheManifestRoundTrip(t *testing.T) {
	home := t.TempDir()
	fixtureHome(t, home)
	out := filepath.Join(t.TempDir(), "staging")
	res, err := Run(Options{
		Home: home, Out: out, Command: writeInstaller(t, relocInstaller), ScanContentRefs: true,
	})
	must(t, err)
	read, err := ReadManifest(out)
	must(t, err)
	if read.Relocatable != res.Manifest.Relocatable || read.RefScan != res.Manifest.RefScan {
		t.Errorf("round trip lost the relocation verdict: %v/%q vs %v/%q",
			read.Relocatable, read.RefScan, res.Manifest.Relocatable, res.Manifest.RefScan)
	}
	if len(read.AbsoluteRefs) != len(res.Manifest.AbsoluteRefs) {
		t.Errorf("round trip lost references: %d vs %d",
			len(read.AbsoluteRefs), len(res.Manifest.AbsoluteRefs))
	}
}

// A manifest written before these fields existed reads back as NOT relocatable. That is the
// fail-safe default the contract depends on: an older entry must never be moved to a home it was
// not captured in just because nobody had thought about the question yet.
func TestAManifestWithoutTheFieldsIsNotRelocatable(t *testing.T) {
	dir := t.TempDir()
	old := `{"schema":1,"home":"/home/agent","platform":"linux/arm64","surfaces":[".local"],` +
		`"excluded":[],"entries":[],"absoluteRefs":[]}`
	must(t, os.WriteFile(ManifestPath(dir), []byte(old), 0o644))
	m, err := ReadManifest(dir)
	must(t, err)
	if m.Relocatable {
		t.Error("a manifest predating the relocation fields reads back as relocatable")
	}
	if m.RefScan != "" {
		t.Errorf("refScan = %q, want empty", m.RefScan)
	}
}

// The one property the fixture cannot reach: a reference split across two read windows. A
// chunked search that forgets to carry the tail forward finds nothing here and everything in
// every other test, which is exactly how this bug ships.
func TestAReferenceStraddlingTheScanWindowIsFound(t *testing.T) {
	home := "/Users/Shared/yolo-captures/probetool/home"
	path := filepath.Join(t.TempDir(), "straddle")
	// Pad so the home string starts three bytes before the first window's end.
	pad := bytes.Repeat([]byte("x"), scanChunk-3)
	must(t, os.WriteFile(path, append(append(pad, []byte(home)...), []byte("/tail\n")...), 0o644))

	found, binary, err := fileHasPrefix(path, []byte(home))
	must(t, err)
	if !found {
		t.Errorf("a reference straddling the %d-byte read window was missed", scanChunk)
	}
	if binary {
		t.Error("a file of xs and a path read as binary")
	}
}

// The NUL sniff looks at the file's OPENING bytes, git-style. A NUL far past that window does not
// make a text file binary, and a short first read must not shift the window.
func TestBinarySniffLooksOnlyAtTheOpeningBytes(t *testing.T) {
	home := "/Users/Shared/yolo-captures/probetool/home"
	dir := t.TempDir()

	late := filepath.Join(dir, "late-nul")
	body := append(bytes.Repeat([]byte("x"), binarySniff+10), 0)
	must(t, os.WriteFile(late, append(body, []byte(home)...), 0o644))
	found, binary, err := fileHasPrefix(late, []byte(home))
	must(t, err)
	if !found || binary {
		t.Errorf("late NUL: found=%v binary=%v, want true/false", found, binary)
	}

	early := filepath.Join(dir, "early-nul")
	must(t, os.WriteFile(early, append([]byte("ELF\x00\x00"), []byte(home)...), 0o644))
	found, binary, err = fileHasPrefix(early, []byte(home))
	must(t, err)
	if !found || !binary {
		t.Errorf("early NUL: found=%v binary=%v, want true/true", found, binary)
	}
}
