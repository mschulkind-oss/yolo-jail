package runcmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestRelayReapOrphansCnameFold checks the run-path backstop reap decision: the
// current jail's just-ensured relay is spared even though its cname is not in the
// live-container set (Python folds `{cname}` into `live_jails`), a live sibling's
// relay is spared, and only an orphan whose hash matches no live jail (and older
// than the grace floor) is reaped. Mirrors run_cmd.py:2760-2771.
func TestRelayReapOrphansCnameFold(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * time.Hour)

	currentName := "yolo-current-1111" // this jail — NOT in the live set yet
	liveSibling := "yolo-sibling-2222" // a running sibling jail
	orphanName := "yolo-orphan-3333"   // dead jail, relay leaked

	writePid := func(cname string) string {
		p := filepath.Join(base, "yolo-broker-relay-"+relayShortHash(cname)+".pid")
		if err := os.WriteFile(p, []byte("123\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Age past the grace floor so mtime alone doesn't spare it.
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		return p
	}
	currentPid := writePid(currentName)
	siblingPid := writePid(liveSibling)
	orphanPid := writePid(orphanName)

	o := &Options{Now: func() time.Time { return now }}
	fillDefaults(o)
	o.Now = func() time.Time { return now }

	// Live set contains only the sibling; the current jail is folded in by cname.
	live := map[string]struct{}{liveSibling: {}}
	reaped := o.relayReapOrphansIn(base, true, live, currentName)

	if !reflect.DeepEqual(reaped, []string{orphanPid}) {
		t.Errorf("reaped %v, want [%s]", reaped, orphanPid)
	}
	// The current jail's relay pid file survives (cname fold).
	if _, err := os.Stat(currentPid); err != nil {
		t.Error("current jail's relay must be spared")
	}
	// The live sibling's relay pid file survives.
	if _, err := os.Stat(siblingPid); err != nil {
		t.Error("live sibling's relay must be spared")
	}
	// The orphan's pid file is removed (apply=true).
	if _, err := os.Stat(orphanPid); err == nil {
		t.Error("orphan relay pid file should be removed")
	}
}

// TestRelayReapOrphansUnknownLivenessDeclines checks the fail-safe polarity: when
// liveness cannot be enumerated (known==false), the sweep reaps nothing — unknown
// must never read as "nothing live". Mirrors _relay_reap_orphans(None) → [].
func TestRelayReapOrphansUnknownLivenessDeclines(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	orphan := filepath.Join(base, "yolo-broker-relay-"+relayShortHash("yolo-dead-9999")+".pid")
	if err := os.WriteFile(orphan, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	o := &Options{Now: func() time.Time { return now }}
	fillDefaults(o)
	o.Now = func() time.Time { return now }

	reaped := o.relayReapOrphansIn(base, false, nil, "yolo-current-0000")
	if len(reaped) != 0 {
		t.Errorf("unknown liveness reaped %v, want none", reaped)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Error("pid file must survive when liveness is unknown")
	}
}
