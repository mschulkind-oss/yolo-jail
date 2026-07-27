package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Unit tests for the writable_home_dirs deriver + validator. Unlike
// cache_relocations these read the merged config (safe at any scope) and touch
// no filesystem, so they build the value directly rather than isolating HOME.

// whdConfig builds a config with a writable_home_dirs value set to v.
func whdConfig(v any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set(writableHomeDirsKey, v)
	return m
}

func TestWritableHomeDirsAbsent(t *testing.T) {
	m := jsonx.NewOrderedMap()
	m.Set("packages", []any{"htop"})
	if got := WritableHomeDirs(m); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	// A present-but-null value is also nothing.
	if got := WritableHomeDirs(whdConfig(nil)); got != nil {
		t.Errorf("null value: expected nil, got %v", got)
	}
}

func TestWritableHomeDirsHappyPath(t *testing.T) {
	got := WritableHomeDirs(whdConfig([]any{".pi-lens", ".foo/bar"}))
	want := []string{".foo/bar", ".pi-lens"} // sorted
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWritableHomeDirsCleansAndDedups(t *testing.T) {
	// "./x" and "x/" both clean to "x"; "a/../b" cleans to "b".
	got := WritableHomeDirs(whdConfig([]any{"./x", "x/", "a/../b", "b"}))
	want := []string{"b", "x"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWritableHomeDirsDropsInvalid(t *testing.T) {
	// Every invalid entry is dropped by the deriver (and errored by validate).
	got := WritableHomeDirs(whdConfig([]any{
		"/etc/passwd", // absolute
		"../escape",   // escapes home
		".config/foo", // reserved first segment
		".ssh/keys",   // reserved first segment
		"go/pkg",      // reserved first segment
		"hi:ro",       // ':' mount-option footgun
		"",            // empty
		42,            // not a string
		".pi-lens",    // the one valid entry
	}))
	want := []string{".pi-lens"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestValidateWritableHomeDirsReportsEachProblem(t *testing.T) {
	var errs []string
	validateWritableHomeDirs(whdConfig([]any{
		"/etc", "../x", ".cache/z", "ok:bad", "", ".pi-lens",
	}), &errs)
	// 5 bad entries → 5 errors; the one good entry produces none.
	if len(errs) != 5 {
		t.Fatalf("expected 5 errors, got %d: %v", len(errs), errs)
	}
	// Each error names its index and reason class.
	joined := ""
	for _, e := range errs {
		joined += e + "\n"
	}
	for _, want := range []string{"[0]", "absolute", "[1]", "escape", "[2]", "already managed", "[3]", "mount option", "[4]", "empty"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors missing %q:\n%s", want, joined)
		}
	}
}

func TestValidateWritableHomeDirsNotAList(t *testing.T) {
	var errs []string
	validateWritableHomeDirs(whdConfig("just-a-string"), &errs)
	if len(errs) != 1 || !strings.Contains(errs[0], "expected a list") {
		t.Errorf("expected one 'expected a list' error, got %v", errs)
	}
}

// reservedHomeSegments must cover the base overlays, single-file mounts,
// symlinks AND every agent overlay dir. Spot-check representatives from each
// class so a future overlay rename can't silently open a clobber.
func TestReservedHomeSegments(t *testing.T) {
	reserved := reservedHomeSegments()
	for _, seg := range []string{
		".npm-global", ".local", "go", ".config", ".cache", ".ssh", // base
		".bash_history", ".yolo-installed-lsps", // single-file
		".gitconfig", ".bashrc", ".claude.json", // symlinks
		".claude", ".copilot", ".gemini", ".pi", ".codex", // agent overlays
	} {
		if _, ok := reserved[seg]; !ok {
			t.Errorf("reserved set missing %q", seg)
		}
	}
	// A path that is NOT ours must be allowed.
	if _, ok := reserved[".pi-lens"]; ok {
		t.Errorf(".pi-lens must not be reserved")
	}
}

// F6 revisited, and the finding does NOT hold as stated: these unions SHOULD span
// every known agent, not the selected set.
//
// The claim was that agents.AllOverlayDirs being a union over all specs is a
// correctness bug that D5 (no agent by default) must fix. It is the opposite. These
// are RESERVATION lists — what a user's host_files/writable_home_dirs entry may not
// claim. If they were selection-gated, the same committed config would validate in a
// jail that selects claude and fail in one that does not, so a repo's config would be
// portable only by accident.
//
// The other all-agents union (storage.EnsureGlobalStorage) is right for the same kind
// of reason: it pre-creates GlobalHome MOUNTPOINTS, and the OCI runtime cannot mkdir
// inside a :ro bind, so a dir any FUTURE jail might mount over has to exist now.
//
// This test pins the intent so the "fix" is not applied later by someone reading the
// finding without the reasoning.
func TestReservedHomeDirsSpanAllAgentsNotSelected(t *testing.T) {
	dirs := reservedHomeDirs()
	// pi and codex are not in DefaultAgents, but their overlay dirs must still be
	// reserved: validation cannot depend on which agents this jail happens to select.
	for _, want := range []string{".claude", ".pi", ".codex", ".copilot"} {
		if _, ok := dirs[want]; !ok {
			t.Errorf("%s not reserved — reservation must not depend on agent selection", want)
		}
	}
}
