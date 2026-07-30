package packsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A missing lockfile is the NORMAL state before the first install, not an error:
// every caller should be able to treat "nothing locked yet" as ordinary.
func TestLoadLockMissingIsEmptyNotAnError(t *testing.T) {
	l, err := LoadLock(filepath.Join(t.TempDir(), "packs.lock.json"))
	if err != nil {
		t.Fatalf("a missing lockfile must not error: %v", err)
	}
	if len(l.Packs) != 0 {
		t.Errorf("expected an empty lock, got %v", l.Packs)
	}
}

func TestLockRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packs.lock.json")
	l := &Lock{}
	l.Set(LockEntry{Name: "acme", Source: "git+https://h/o/r//p?ref=main",
		Commit: "abc123", Ref: "main"})
	l.Set(LockEntry{Name: "local", Source: "file:///p"})
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}

	got, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := got.Get("acme")
	if !ok {
		t.Fatal("acme not in the reloaded lock")
	}
	// The pair is the point: ref is what was ASKED FOR, commit is what was GOT.
	if e.Ref != "main" || e.Commit != "abc123" {
		t.Errorf("entry = %+v, want ref=main commit=abc123", e)
	}
	// A local pack has no commit — inventing one would fake a pin.
	if lo, _ := got.Get("local"); lo.Commit != "" {
		t.Errorf("local pack should have no commit, got %q", lo.Commit)
	}
}

// The file is diffed by humans and may be committed to a dotfiles repo, so a rewrite
// must be byte-stable rather than reordering keys and burying the real change.
func TestLockSaveIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) []byte {
		l := &Lock{}
		for _, n := range []string{"zeta", "alpha", "mid"} {
			l.Set(LockEntry{Name: n, Source: "file:///" + n})
		}
		p := filepath.Join(dir, name)
		if err := l.Save(p); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	a, b := write("a.json"), write("b.json")
	if string(a) != string(b) {
		t.Errorf("Save is not deterministic:\n%s\n---\n%s", a, b)
	}
	if !strings.HasSuffix(string(a), "\n") {
		t.Error("lockfile should end with a newline")
	}
	// Sorted, so a diff shows only what changed.
	if strings.Index(string(a), "alpha") > strings.Index(string(a), "zeta") {
		t.Errorf("keys are not sorted:\n%s", a)
	}
}

// A newer schema must ERROR rather than be misread: guessing at an unknown format is
// how a lockfile ends up describing the wrong pack.
func TestLoadLockRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packs.lock.json")
	if err := os.WriteFile(path, []byte(`{"schema": 99, "packs": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadLock(path)
	if err == nil {
		t.Fatal("expected an error for a newer schema")
	}
	if !strings.Contains(err.Error(), "upgrade yolo") {
		t.Errorf("error should say what to do: %v", err)
	}
}

// A corrupt lockfile must say how to recover: it is regenerable, so "delete it" is
// the right advice and the user cannot be expected to infer that.
func TestLoadLockCorruptExplainsRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packs.lock.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadLock(path)
	if err == nil {
		t.Fatal("expected an error for a corrupt lockfile")
	}
	if !strings.Contains(err.Error(), "delete it") {
		t.Errorf("error should explain recovery: %v", err)
	}
}

// Pruning reports what it removed: a lock entry vanishing means a pack left the
// config and its content is about to stop being delivered, which is worth seeing.
func TestPruneReportsRemoved(t *testing.T) {
	l := &Lock{}
	for _, n := range []string{"keep", "gone", "alsogone"} {
		l.Set(LockEntry{Name: n, Source: "file:///" + n})
	}
	removed := l.Prune([]string{"keep"})
	if len(removed) != 2 || removed[0] != "alsogone" || removed[1] != "gone" {
		t.Errorf("removed = %v, want [alsogone gone] sorted", removed)
	}
	if _, ok := l.Get("keep"); !ok {
		t.Error("configured pack was pruned")
	}
}

// DRIFT is the important one. Launch resolves from the LOCK, so an edited ref in
// config would otherwise appear to do nothing until someone ran install — the most
// confusing behavior available. Drift makes the staleness visible.
func TestDriftFromDetectsEditedAddress(t *testing.T) {
	l := &Lock{}
	l.Set(LockEntry{Name: "acme", Source: "git+https://h/o/r//p?ref=v1", Commit: "aaa", Ref: "v1"})
	l.Set(LockEntry{Name: "same", Source: "file:///same"})

	drift := l.DriftFrom(map[string]string{
		"acme":    "git+https://h/o/r//p?ref=v2", // ref edited
		"same":    "file:///same",                // unchanged
		"unknown": "file:///new",                 // not locked yet: install's job, not drift
	})
	if len(drift) != 1 {
		t.Fatalf("drift = %+v, want exactly the edited pack", drift)
	}
	if drift[0].Name != "acme" || !strings.Contains(drift[0].WantedSource, "v2") {
		t.Errorf("drift = %+v", drift[0])
	}
}

func TestLockPathSitsBesideUserConfig(t *testing.T) {
	got := LockPath("/home/me/.config/yolo-jail/config.jsonc")
	if got != "/home/me/.config/yolo-jail/packs.lock.json" {
		t.Errorf("LockPath = %q", got)
	}
}

// HostAccessApproved is the superset check that decides whether a fetched pack's
// current host-access claims are all already approved (no re-prompt) or include a
// new one (re-prompt). The rule the whole approval model turns on.
func TestHostAccessApprovedSupersetRule(t *testing.T) {
	e := LockEntry{
		Name:               "acme",
		ApprovedHostAccess: []string{"mount refs -> /ctx/refs", "reads-host .config/acme/key"},
	}
	cases := []struct {
		name string
		want []string
		ok   bool
	}{
		{"reads nothing is always approved", nil, true},
		{"exact match approved", []string{"mount refs -> /ctx/refs", "reads-host .config/acme/key"}, true},
		{"a narrowed subset approved", []string{"mount refs -> /ctx/refs"}, true},
		{"a NEW claim not approved", []string{"mount refs -> /ctx/refs", "reads-host .ssh/id_ed25519"}, false},
		{"a wholly different claim not approved", []string{"installer https://x/i.sh"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.HostAccessApproved(tc.want); got != tc.ok {
				t.Errorf("HostAccessApproved(%v) = %v, want %v", tc.want, got, tc.ok)
			}
		})
	}

	// An entry with NO approvals grants nothing but the empty set.
	empty := LockEntry{Name: "x"}
	if empty.HostAccessApproved([]string{"mount a -> /ctx/a"}) {
		t.Error("an unapproved entry must not approve any host-access claim")
	}
	if !empty.HostAccessApproved(nil) {
		t.Error("an unapproved entry must still approve the empty set (reads nothing)")
	}
}

// The approval fields round-trip through save/load so an approval survives across
// invocations (the whole point — approve once, trusted until the pin moves).
func TestApprovalFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "packs.lock.json")
	l := &Lock{Schema: LockSchema, Packs: map[string]LockEntry{}}
	l.Set(LockEntry{
		Name: "acme", Source: "git+ssh://h/o/r//p?ref=v1", Commit: "abc123", Ref: "v1",
		ApprovedHostAccess: []string{"mount refs -> /ctx/refs"}, ApprovedAt: "abc123",
	})
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := got.Get("acme")
	if !ok || e.ApprovedAt != "abc123" || len(e.ApprovedHostAccess) != 1 ||
		e.ApprovedHostAccess[0] != "mount refs -> /ctx/refs" {
		t.Errorf("approval did not round-trip: %+v", e)
	}
}
