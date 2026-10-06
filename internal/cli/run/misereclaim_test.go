package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/miseuse"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// misereclaim_test.go pins the launch's half of OQ-DF4 (minimal-disk-footprint.md, ruled
// 2026-10-05): the versions of the shared mise store no jail has used for 30 days are offered at
// a launch like the cache class, through the same offer and the same consent record, and a yes
// makes the slot remove them.

// miseSlotFixture is a host-frame Options under a temp HOME whose tool store holds node/20.1.0,
// which no jail uses, and node/22.5.0, which a fresh record names. Its clock runs two windows
// ahead, since the version directories are made now and a change time cannot be set back.
func miseSlotFixture(t *testing.T) (*Options, func(rel string) bool) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	o := goldenOptions(t.TempDir(), home)
	now := time.Now().Add(2 * miseuse.Window)
	o.Now = func() time.Time { return now }
	// The runtime answers exactly one question — which jails run (none) — and nothing else, so no
	// other class of the slot finds anything to act on.
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if strings.Join(argv, " ") == "podman ps -a --format {{.Names}} {{.State}}" {
			return ExecResult{Ran: true}
		}
		return ExecResult{Ran: false}
	}
	store := paths.GlobalMise()
	for _, rel := range []string{"node/20.1.0", "node/22.5.0"} {
		dir := filepath.Join(store, "installs", filepath.FromSlash(rel), "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "node"), make([]byte, 1000), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(store, miseuse.DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	since := now.Add(-miseuse.Window - 24*time.Hour).UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(store, miseuse.DirName, miseuse.SinceName), []byte(since), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := miseuse.Write(store, miseuse.NewName(), miseuse.Record{
		Workspace: "/home/u/code/a", Recorded: now.Add(-time.Hour), Installs: []string{"node/22.5.0"},
	}); err != nil {
		t.Fatal(err)
	}
	present := func(rel string) bool {
		_, err := os.Lstat(filepath.Join(store, "installs", filepath.FromSlash(rel)))
		return err == nil
	}
	return o, present
}

// TestTheSlotMeasuresAndOnAYesRemovesUnusedToolVersions drives the slot itself: without consent
// it only measures, for the next launch to offer on; with it, the version no jail uses goes and
// the one a jail uses stays. Delete the slot's call and this fails.
func TestTheSlotMeasuresAndOnAYesRemovesUnusedToolVersions(t *testing.T) {
	o, present := miseSlotFixture(t)
	o.runHousekeeping("podman", reclaimConsent{}, testCname)
	m := LastOfferMeasurement(miseVersionsClass)
	if m.Bytes != 1000 || !strings.Contains(m.Detail, "node 20.1.0") {
		t.Fatalf("the slot measured %+v; the next launch offers on this, so without it the offer "+
			"never has a size and never fires", m)
	}
	if !present("node/20.1.0") {
		t.Fatal("the slot removed a version without consent")
	}

	o, present = miseSlotFixture(t)
	var yes reclaimConsent
	yes.grant(miseVersionsClass, false)
	o.runHousekeeping("podman", yes, testCname)
	if present("node/20.1.0") || !present("node/22.5.0") {
		t.Fatalf("with consent: 20.1.0 present=%v, 22.5.0 present=%v; want only the used one kept",
			present("node/20.1.0"), present("node/22.5.0"))
	}
}

// TestAFreshYesReclaimsInThisLaunch: the offer is made from the LAST launch's measurement, whose
// debounce stamp is usually hours old, so a yes given now used to wait up to a day. A yes given
// at this launch's prompt runs its class in this slot; a standing yes keeps the debounce. Both
// offered classes, since they share the consent.
func TestAFreshYesReclaimsInThisLaunch(t *testing.T) {
	t.Run("tool versions", func(t *testing.T) {
		for _, fresh := range []bool{false, true} {
			o, present := miseSlotFixture(t)
			_, done := o.classDebounce("mise-versions")
			done() // measured an hour ago, by the launch whose figure was offered
			var c reclaimConsent
			c.grant(miseVersionsClass, fresh)
			o.measureAndPurgeMiseVersions("podman", c, nil)
			if gone := !present("node/20.1.0"); gone != fresh {
				t.Errorf("fresh=%v: removed=%v; a fresh yes must reclaim now, a standing one waits "+
					"out the debounce", fresh, gone)
			}
		}
	})
	t.Run("cache files", func(t *testing.T) {
		for _, fresh := range []bool{false, true} {
			home := t.TempDir()
			t.Setenv("HOME", home)
			o := goldenOptions(t.TempDir(), home)
			o.Now = time.Now
			old := filepath.Join(paths.GlobalStorage(), "cache", "uv", "old")
			if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			past := time.Now().Add(-60 * 24 * time.Hour)
			_ = os.Chtimes(old, past, past)
			_, done := o.classDebounce("cache")
			done()
			var c reclaimConsent
			c.grant(cachePurgeClass, fresh)
			o.measureAndPurgeCache(c, nil)
			_, err := os.Stat(old)
			if gone := os.IsNotExist(err); gone != fresh {
				t.Errorf("fresh=%v: removed=%v; a fresh yes must reclaim now", fresh, gone)
			}
		}
	})
}

// TestTheOfferAsksAboutToolVersions: the class is in the offered tier — offered once it reaches
// the threshold, in the one prompt every offered class shares, and a yes is recorded as the
// class's standing answer and handed to this launch's slot as fresh.
func TestTheOfferAsksAboutToolVersions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(t.TempDir(), home)
	o.Now = time.Now
	o.IsTTYStdout = func() bool { return true }
	var out bytes.Buffer
	o.Stdout, o.Stdin = &out, strings.NewReader("y\n")
	RecordOfferMeasurement(miseVersionsClass, offerMeasurement{Bytes: 2 << 30, Detail: "3 versions: python 3.11.9, node 20.1.0, go 1.24.1", When: time.Now()})
	RecordOfferMeasurement(cachePurgeClass, offerMeasurement{Bytes: 5 << 30, Detail: "120 files", When: time.Now()})

	c := o.maybeOfferReclaim()
	prompt := out.String()
	if strings.Count(prompt, "Reclaim now?") != 1 {
		t.Errorf("two classes due at one launch must share ONE prompt:\n%s", prompt)
	}
	for _, want := range []string{miseVersionsClass, "python 3.11.9", "2.0 GiB", cachePurgeClass} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not show %q:\n%s", want, prompt)
		}
	}
	for _, class := range []string{miseVersionsClass, cachePurgeClass} {
		if !c.has(class) || !c.freshFor(class) {
			t.Errorf("%s: consent %v / fresh %v after a yes", class, c.has(class), c.freshFor(class))
		}
		if OfferAnswerFor(class).Answer != OfferYes {
			t.Errorf("%s: the yes was not recorded, so the next launch would ask again", class)
		}
	}

	// The next launch: a standing yes, not a fresh one, and no prompt.
	out.Reset()
	c = o.maybeOfferReclaim()
	if out.Len() != 0 || !c.has(miseVersionsClass) || c.freshFor(miseVersionsClass) {
		t.Errorf("after a yes the class must be automatic, unasked and not fresh: prompt %q, %+v", out.String(), c)
	}
}

// TestBelowTheThresholdToolVersionsAreNotOffered: the ruling's "once they total 1 GiB".
func TestBelowTheThresholdToolVersionsAreNotOffered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(t.TempDir(), home)
	o.Now = time.Now
	o.IsTTYStdout = func() bool { return true }
	var out bytes.Buffer
	o.Stdout, o.Stdin = &out, strings.NewReader("y\n")
	RecordOfferMeasurement(miseVersionsClass, offerMeasurement{Bytes: OfferThreshold - 1, Detail: "1 version: node 20.1.0", When: time.Now()})
	if c := o.maybeOfferReclaim(); out.Len() != 0 || c.has(miseVersionsClass) {
		t.Fatalf("a class under the threshold was offered: %q", out.String())
	}
}

// TestTheToolVersionClassIsTheHostsAndHonorsTheOptOut: a jail cannot see the host's running jails,
// and the automatic reapers' opt-out (which the integration suite sets) covers this class too.
func TestTheToolVersionClassIsTheHostsAndHonorsTheOptOut(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"in a jail": {"YOLO_VERSION": "0.0.0-test"},
		"opted out": {autoReapOptOutEnv: "1"},
	} {
		o, present := miseSlotFixture(t)
		o.Getenv = func(k string) string { return env[k] }
		var c reclaimConsent
		c.grant(miseVersionsClass, true)
		o.measureAndPurgeMiseVersions("podman", c, nil)
		if !present("node/20.1.0") || LastOfferMeasurement(miseVersionsClass).Bytes != 0 {
			t.Errorf("%s: the class ran", name)
		}
	}
	// A Mac's jails keep their store in the container VM, not in the state dir.
	o, present := miseSlotFixture(t)
	o.IsMacOS = true
	var c reclaimConsent
	c.grant(miseVersionsClass, true)
	o.measureAndPurgeMiseVersions("podman", c, nil)
	if !present("node/20.1.0") || LastOfferMeasurement(miseVersionsClass).Bytes != 0 {
		t.Error("on a Mac the class judged the state dir's store, which no jail there uses")
	}
}

// TestADeclinedToolVersionPassOffersNothingAndRetries: when the records cannot answer, the pass
// leaves nothing to offer and does not stamp its debounce, so the next launch asks again.
func TestADeclinedToolVersionPassOffersNothingAndRetries(t *testing.T) {
	o, present := miseSlotFixture(t)
	RecordOfferMeasurement(miseVersionsClass, offerMeasurement{Bytes: 5 << 30, Detail: "stale"})
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} }
	var c reclaimConsent
	c.grant(miseVersionsClass, false)
	o.measureAndPurgeMiseVersions("podman", c, nil)
	if !present("node/20.1.0") {
		t.Fatal("a pass that could not ask the runtime removed a version")
	}
	if m := LastOfferMeasurement(miseVersionsClass); m.Bytes != 0 || !strings.HasPrefix(m.Detail, "declined:") {
		t.Errorf("a declined pass left %+v to offer", m)
	}
	if due, _ := o.classDebounce("mise-versions"); !due {
		t.Error("a declined pass stamped its debounce: one unreachable runtime would then cost a day")
	}
}

// TestAHostLaunchStartsTheRecordsClock: the sweep judges nothing until 30 days after the first
// HOST launch that runs recording jails, and only a host launch may start that clock — an in-jail
// launch (a development jail on a newer tree than the host's yolo), a sealed build (its own
// store) and a Mac (a VM volume the host never judges) leave it alone.
func TestAHostLaunchStartsTheRecordsClock(t *testing.T) {
	mark := func(mutate func(o *Options)) bool {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		o := goldenOptions(t.TempDir(), home)
		at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
		o.Now = func() time.Time { return at }
		mutate(o)
		store := filepath.Join(t.TempDir(), "mise")
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		o.markMiseUseRecording(store)
		c, err := miseuse.ReadAll(store)
		if err != nil {
			t.Fatal(err)
		}
		return c.Since.Equal(at)
	}
	if !mark(func(*Options) {}) {
		t.Fatal("a host launch did not start the use record's clock: the sweep would wait forever")
	}
	for name, mutate := range map[string]func(o *Options){
		"in a jail": func(o *Options) {
			o.Getenv = func(k string) string { return map[string]string{"YOLO_VERSION": "0.0.0-test"}[k] }
		},
		"sealed build": func(o *Options) { o.Sealed = true },
		"on a Mac":     func(o *Options) { o.IsMacOS = true },
	} {
		if mark(mutate) {
			t.Errorf("%s: the launch started the clock", name)
		}
	}
}

// TestTheLaunchStartsTheRecordsClockForTheStoreItBinds pins the call site: runContainer marks the
// store it is about to bind, after the sealed build has picked its own. Without the call no host
// ever starts the clock, and the sweep waits forever with every unit test green.
func TestTheLaunchStartsTheRecordsClockForTheStoreItBinds(t *testing.T) {
	src, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	fn := afterFunc(string(src), "func (o *Options) runContainer(")
	assign := strings.Index(fn, "cacheDir, miseStore := paths.GlobalCache(), jailMiseStoreDir(o.inJail())")
	sealed := strings.Index(fn, "sealedStores(o.Workspace)")
	call := strings.Index(fn, "\to.markMiseUseRecording(miseStore)\n")
	if assign < 0 || sealed < 0 || call < 0 || call < sealed {
		t.Fatalf("runContainer no longer marks the store it binds after the sealed build's choice "+
			"(assign %d, sealed %d, mark %d)", assign, sealed, call)
	}
}
