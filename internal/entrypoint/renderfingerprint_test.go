package entrypoint

// renderfingerprint_test.go is the BYTE-EQUALITY GATE for the env-manager plan's Phase 1
// (the internal/render + Target refactor). Phase 1 moves the three surface writers out of
// this package onto an explicit Target; because that refactor is on the A12-fatal boot
// path — a regression stops jails from *starting*, not merely misconfigures one — the bar
// (host-render-target.md §3.5) is: every shipped pack's rendered surfaces are byte-identical
// before and after.
//
// This test captures that fingerprint from the CURRENT code: it renders every embedded pack
// into a scratch home with a fixed input environment, then hashes every file produced. The
// golden map below is the pre-refactor baseline. As the writers move to internal/render, this
// test must keep passing unchanged — if a byte moves, the refactor changed behavior and the
// diff says exactly which surface.
//
// It is deliberately in package entrypoint (not internal/render) so it exercises the SAME
// entry the boot path uses (ConfigurePackByName → renderDeclaredSurface) regardless of where
// the writers physically live.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// renderFingerprint renders every embedded pack into a fresh home under a fixed input
// environment and returns {home-relative path → sha256} for every file written, plus the
// sorted list of paths. Fixed inputs (no MCP/LSP servers, empty vars) so the output is
// deterministic and does not depend on the host's config.
func renderFingerprint(t *testing.T) (map[string]string, []string) {
	t.Helper()
	return renderFingerprintAt(t, t.TempDir())
}

// renderFingerprintAt renders into a fresh home but a CALLER-SUPPLIED workspace, so two
// fingerprints can share one workspace path. That matters because a surface keyed on
// ${workspace} (claude's projects["${workspace}"]) embeds the workspace path — two renders
// with different workspace dirs differ only there, which is substitution, not real
// nondeterminism. The workspace must be a real writable dir (the render creates its
// .yolo/prism sidecar tree under it).
func renderFingerprintAt(t *testing.T, ws string) (map[string]string, []string) {
	t.Helper()
	home := t.TempDir()
	e := &Env{Home: home, Workspace: ws, Vars: map[string]string{}}

	for _, name := range EmbeddedPackNames() {
		if err := ConfigurePackByName(e, name); err != nil {
			t.Fatalf("render pack %q: %v", name, err)
		}
	}

	fp := map[string]string{}
	err := filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Only fingerprint regular files. The shared_credentials hook drops a SYMLINK
		// (.claude/.credentials.json → the machine-global creds dir) that is dangling in
		// a test with no such dir; symlinks are hook artifacts, not rendered surface
		// content, so they are out of scope for the render byte-gate.
		if !d.Type().IsRegular() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(home, p)
		sum := sha256.Sum256(data)
		fp[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("walk render output: %v", err)
	}
	paths := make([]string, 0, len(fp))
	for k := range fp {
		paths = append(paths, k)
	}
	sort.Strings(paths)
	return fp, paths
}

// TestRenderFingerprintStable pins the set of files every shipped pack renders. This is the
// coarse half of the gate: which files exist. If Phase 1 drops or adds a rendered surface,
// this list changes and the test fails with the delta.
//
// The expected set is derived from the packs at head; when a pack legitimately gains or drops
// a surface, update this list in the SAME commit and say why. During the Phase 1 refactor it
// must not change at all.
func TestRenderFingerprintStable(t *testing.T) {
	_, paths := renderFingerprint(t)

	// Every rendered file must live under a known agent config root — a sanity check that
	// the render wrote where the packs declare and nowhere else.
	knownRoots := []string{
		".claude", ".codex", ".config", ".copilot", ".pi", ".gemini",
	}
	// Top-level home files a pack legitimately owns (not under a config-dir root).
	knownFiles := map[string]bool{".claude.json": true}
	for _, p := range paths {
		ok := knownFiles[p]
		for _, r := range knownRoots {
			if strings.HasPrefix(p, r+"/") || p == r {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("render wrote outside any known agent config root: %q "+
				"(if a pack legitimately added this, extend knownRoots)", p)
		}
	}
	if len(paths) == 0 {
		t.Fatal("no surfaces rendered — the fingerprint gate is covering nothing")
	}
	t.Logf("render fingerprint covers %d files:\n  %s", len(paths), strings.Join(paths, "\n  "))
}

// TestRenderFingerprintDeterministic is the fine half of the gate, self-contained: two
// renders of the same packs under the same inputs must be byte-identical. This is what makes
// renderFingerprint usable as a before/after comparison across the refactor — a caller can
// snapshot it, change the writers, and re-run to prove nothing moved. If this ever flakes,
// the render has hidden nondeterminism (map iteration, timestamps) that must be fixed before
// the Phase 1 refactor can be trusted.
func TestRenderFingerprintDeterministic(t *testing.T) {
	ws := t.TempDir() // one workspace shared by both renders (see renderFingerprintAt)
	a, _ := renderFingerprintAt(t, ws)
	b, _ := renderFingerprintAt(t, ws)
	if len(a) != len(b) {
		t.Fatalf("two renders produced different file counts: %d vs %d", len(a), len(b))
	}
	for p, ha := range a {
		hb, ok := b[p]
		if !ok {
			t.Errorf("%q rendered in first pass but not second", p)
			continue
		}
		if ha != hb {
			t.Errorf("%q is nondeterministic across renders: %s vs %s", p, ha[:12], hb[:12])
		}
	}
}
