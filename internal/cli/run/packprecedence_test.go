package run

// packprecedence_test.go pins the LAUNCH's and the BOOT's call sites of the one pack order
// (config.PackSelection.Packs, docs/plans/notch-convergence.md OQ-NC4, ruled A): config order as
// written, then the closure's additions, then the conventional local pack last. The launch used
// to put the embedded entries first, and the boot read the tree it was handed alphabetically
// (os.ReadDir), so one key had a different winner in each (row B2).

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// precedenceHome selects a local pack "zeta" that needs hello-daemon, then the embedded
// guardrails pack, and gives the home a conventional local pack. The three orders the notches
// used differ over it: embedded-first (guardrails, zeta, local, hello-daemon), alphabetical
// (guardrails, hello-daemon, local, zeta) and the host's (zeta, guardrails, local, hello-daemon).
func precedenceHome(t *testing.T) {
	t.Helper()
	home := packHome(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	zeta := localPackDir(t, "zeta")
	writePack(t, zeta, `{"name":"zeta","needs":[{"pack":"hello-daemon"}]}`)
	writeUserPacks(t, home, `["file://`+zeta+`", "guardrails"]`)
	local := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, local, `{"name":"local"}`)
}

var wantPrecedence = []string{"zeta", "guardrails", "hello-daemon", "local"}

func packNames(packs []*packload.Pack) []string {
	var out []string
	for _, p := range packs {
		out = append(out, p.Name)
	}
	return out
}

// THE LAUNCH STAGES IN THE ONE ORDER, and records it in the tree for every later reader.
func TestTheLaunchOrdersPacksAsTheConfigListsThem(t *testing.T) {
	precedenceHome(t)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	tree, loaded, _, err := o.stagePacks("yolo-test-precedence")
	if err != nil {
		t.Fatal(err)
	}
	if got := packNames(loaded); !slices.Equal(got, wantPrecedence) {
		t.Errorf("the launch loaded %v, want %v", got, wantPrecedence)
	}
	reread, err := loadPackTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	if got := packNames(reread); !slices.Equal(got, wantPrecedence) {
		t.Errorf("an attach reads the tree as %v, want %v", got, wantPrecedence)
	}
}

// THE BOOT READS THE ORDER THE LAUNCH RECORDED. Driven over a tree the launch itself staged, so it
// fails if either side stops using the record: the boot names a configured pack by its directory
// (StagedSlug, which is the name for every pack here), so only the order is compared.
func TestTheBootReadsThePackOrderTheLaunchRecorded(t *testing.T) {
	strictPackloadReads(t)
	precedenceHome(t)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf()}
	tree, loaded, _, err := o.stagePacks("yolo-test-precedence-boot")
	if err != nil {
		t.Fatal(err)
	}
	// LoadJailPacks switches the process to tolerant manifest reads; strictPackloadReads
	// restores the host-side test process to strict reads when this test ends.
	e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": tree})
	e.Stderr = discardBuf()
	booted, err := entrypoint.LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	var launchRoots, bootRoots []string
	for _, p := range loaded {
		launchRoots = append(launchRoots, p.Root)
	}
	for _, p := range booted {
		bootRoots = append(bootRoots, p.Root)
	}
	if !slices.Equal(bootRoots, launchRoots) {
		t.Errorf("the boot loaded\n  %v\nwant the launch's order\n  %v", bootRoots, launchRoots)
	}
	if got := packNames(booted); !slices.Equal(got, wantPrecedence) {
		t.Errorf("the boot's packs are %v, want %v", got, wantPrecedence)
	}
}
