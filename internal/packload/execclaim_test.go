package packload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// The executables claim is what remains of `allow_exec`: the gate is gone, the FACT it
// surfaced is not, and a mode bit is the one property of a pack that no manifest line can
// tell a reader about.

// execPack writes a pack tree and returns a *Pack for it. modes are pack-relative paths
// to file modes.
func execPack(t *testing.T, modes map[string]os.FileMode) *Pack {
	t.Helper()
	root := t.TempDir()
	for rel, mode := range modes {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil { // umask does not get a vote
			t.Fatal(err)
		}
	}
	return &Pack{Name: "p", Root: root, Decl: &packdecl.Manifest{}}
}

// execClaim returns the one executables claim in a footprint, or nil.
func execClaim(fp Footprint) *Claim {
	for i := range fp.Claims {
		if fp.Claims[i].Kind == ExecutablesClaimKind {
			return &fp.Claims[i]
		}
	}
	return nil
}

func TestFootprintReportsShippedExecutables(t *testing.T) {
	p := execPack(t, map[string]os.FileMode{
		"skills/s/references/check.sh": 0o755,
		"skills/s/SKILL.md":            0o644,
		"README.md":                    0o644,
	})
	c := execClaim(FootprintOf(p))
	if c == nil {
		t.Fatal("no executables claim for a pack shipping one")
	}
	if c.Target != "1 file" {
		t.Errorf("Target = %q, want %q", c.Target, "1 file")
	}
	if c.Detail != "skills/s/references/check.sh" {
		t.Errorf("Detail = %q, want the executable's path", c.Detail)
	}
	// NOT review-worthy: review-worthy claims reach the launch disclosure, which is about
	// boundary crossings. Flagging this would put an unchanging line in front of the user
	// on every launch — see ExecutablesClaimKind.
	if c.ReviewWorthy || c.RunsHostCode {
		t.Errorf("claim must not be flagged (ReviewWorthy=%v RunsHostCode=%v): shipping a "+
			"script is not a crossing, and the launch disclosure is for crossings",
			c.ReviewWorthy, c.RunsHostCode)
	}
}

// A content-only pack claims nothing here — the common case, and the one that decides
// whether the claim is signal or wallpaper.
func TestFootprintSilentWithoutExecutables(t *testing.T) {
	p := execPack(t, map[string]os.FileMode{
		"skills/s/SKILL.md": 0o644,
		"AGENTS.md":         0o644,
	})
	if c := execClaim(FootprintOf(p)); c != nil {
		t.Errorf("content-only pack claimed executables: %+v", c)
	}
}

// Many executables summarize rather than flooding the report: a pack of scripts must not
// push its other claims off the reader's screen.
func TestFootprintCapsTheExecutableList(t *testing.T) {
	modes := map[string]os.FileMode{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		modes["bin/"+n+".sh"] = 0o755
	}
	c := execClaim(FootprintOf(execPack(t, modes)))
	if c == nil {
		t.Fatal("no executables claim")
	}
	if c.Target != "7 files" {
		t.Errorf("Target = %q, want %q", c.Target, "7 files")
	}
	if !strings.Contains(c.Detail, "and 2 more") {
		t.Errorf("Detail should summarize past the cap: %q", c.Detail)
	}
	if strings.Contains(c.Detail, "bin/g.sh") {
		t.Errorf("Detail listed past the cap: %q", c.Detail)
	}
	// Sorted, so `yolo pack footprint` diffs cleanly between two versions of a pack —
	// a walk's directory order is not a guarantee to build that on.
	if !strings.HasPrefix(c.Detail, "bin/a.sh, bin/b.sh") {
		t.Errorf("Detail is not in sorted order: %q", c.Detail)
	}
}

// The claim kind is a DISPLAY LABEL, not a manifest kind. If it ever entered the closed
// registry, `kind: "executables"` would become writable in a pack.json — declaring a fact
// about the tree that yolo reads from the tree — and Collisions would start reporting two
// packs that both ship scripts as a conflict.
func TestExecutablesClaimKindIsNotAManifestKind(t *testing.T) {
	for _, k := range packdecl.KnownKinds() {
		if k == ExecutablesClaimKind {
			t.Fatalf("%q is in the closed kind registry; it must stay a display label, "+
				"as SupersedesClaimKind does", ExecutablesClaimKind)
		}
	}
}
