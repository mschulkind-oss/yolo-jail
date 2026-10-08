package broker

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// settingsFixture is a fake Deps for a LIVE singleton (pid 42, socket present and
// accepting) that is handed a settings file, recorded as having been started with
// startedWith. The file is then rewritten to now, the state a launch leaves after
// writeLoopholeSettings and before its ensure.
func settingsFixture(t *testing.T, startedWith, now string) (Deps, *fakeState, *bytes.Buffer) {
	t.Helper()
	st := &fakeState{alive: map[int]bool{42: true}, reachOK: true, spawnPID: 77}
	deps := newFakeDeps(t, st)
	deps.SettingsPath = filepath.Join(t.TempDir(), "settings.json")
	var out bytes.Buffer
	deps.Out = &out
	writePID(t, deps, 42)
	if err := os.WriteFile(deps.SocketPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if startedWith != "" {
		values, err := parseFlatSettings([]byte(startedWith))
		if err != nil {
			t.Fatal(err)
		}
		writeSettingsRecord(deps, values)
	}
	if err := os.WriteFile(deps.SettingsPath, []byte(now+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A kill takes effect, and a spawn binds its socket — the two facts a restart needs
	// from the OS.
	deps.Kill = func(pid int, sig syscall.Signal) error {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.killed = append(st.killed, struct {
			pid int
			sig syscall.Signal
		}{pid, sig})
		st.alive[pid] = false
		return nil
	}
	deps.Spawn = func(argv []string, _ string) (int, func() bool, error) {
		st.spawnArgv = argv
		st.alive[st.spawnPID] = true
		return st.spawnPID, func() bool { return false }, os.WriteFile(deps.SocketPath, nil, 0o600)
	}
	return deps, st, &out
}

// TestEnsureRestartsALiveDaemonWhoseSettingsChanged is the defect itself (2026-09-28):
// `loopholes.aws-auth.settings.profile` was corrected, the launch rewrote the settings
// file, and the ensure REUSED the live daemon because it asked only whether it was alive.
// Every mint kept failing on the old profile until a manual `yolo host-daemon restart`.
func TestEnsureRestartsALiveDaemonWhoseSettingsChanged(t *testing.T) {
	deps, st, out := settingsFixture(t,
		`{"profile":"hssandbox-admin"}`, `{"profile":"hssandbox-Admin"}`)

	got := EnsureSingleton(deps)

	if got.Stale != nil {
		t.Errorf("Stale = %+v after a restart that succeeded", *got.Stale)
	}
	if len(st.killed) == 0 || st.killed[0].pid != 42 || st.killed[0].sig != syscall.SIGTERM {
		t.Fatalf("killed = %v, want the old daemon (42) sent SIGTERM first — the "+
			"`yolo host-daemon restart` sequence, whose drain lets in-flight requests finish",
			st.killed)
	}
	if len(st.spawnArgv) == 0 {
		t.Fatal("no respawn: the daemon was stopped and nothing replaced it")
	}
	if !got.Started {
		t.Error("Started = false after the ensure spawned the replacement; a caller would ensure " +
			"again and start a second copy beside it")
	}
	if pid, _ := BrokerReadPID(deps); pid != 77 {
		t.Errorf("pid file names %d, want the new daemon 77", pid)
	}
	// The new record describes the NEW settings, so the next launch reuses it.
	if drift, ok := RunningSettingsDrift(deps); !ok || drift.Stale() {
		t.Errorf("after the restart RunningSettingsDrift = %+v (ok=%v), want current", drift, ok)
	}
	line := out.String()
	for _, want := range []string{"Restarting the host-wide daemon", "(profile)", "It is shared"} {
		if !strings.Contains(line, want) {
			t.Errorf("restart line %q lacks %q", line, want)
		}
	}
	for _, value := range []string{"hssandbox-admin", "hssandbox-Admin"} {
		if strings.Contains(line, value) {
			t.Errorf("restart line %q prints the VALUE %q; a setting can be a credential, "+
				"so the line names keys only", line, value)
		}
	}
}

// TestEnsureReusesALiveDaemonWhoseSettingsMatch: an unchanged config is the ordinary
// launch, and it must stay what it was — a reuse, silent, with no signal sent. A restart
// on every launch would be the "take turns killing each other's daemon" shape
// host-daemon-ownership.md §6 warns about.
func TestEnsureReusesALiveDaemonWhoseSettingsMatch(t *testing.T) {
	deps, st, out := settingsFixture(t,
		`{"profile":"p","unnarrowed":false}`, `{"unnarrowed":false,"profile":"p"}`)

	got := EnsureSingleton(deps)

	if len(st.killed) != 0 || len(st.spawnArgv) != 0 {
		t.Errorf("a daemon running the current settings was restarted: killed=%v spawn=%v",
			st.killed, st.spawnArgv)
	}
	if got.Stale != nil || out.Len() != 0 {
		t.Errorf("Stale=%v output=%q, want a silent reuse", got.Stale, out.String())
	}
	if got.Started {
		t.Error("Started = true for a reused daemon; a caller finding it stopped since would not " +
			"ensure again")
	}
}

// TestEnsureRestartsAnUnrecordedDaemonHandedSettings: a daemon started by a yolo that kept
// no record cannot be shown to run the current settings — and that is exactly the daemon
// the maintainer had running when this landed. It is restarted once, saying why, and the
// new one carries a record.
func TestEnsureRestartsAnUnrecordedDaemonHandedSettings(t *testing.T) {
	deps, st, out := settingsFixture(t, "", `{"profile":"p"}`)

	EnsureSingleton(deps)

	if len(st.spawnArgv) == 0 {
		t.Fatal("an unrecorded daemon handed settings was reused")
	}
	if !strings.Contains(out.String(), "predates yolo recording") {
		t.Errorf("restart line %q does not say why", out.String())
	}
	if _, err := os.Stat(settingsRecordPath(deps)); err != nil {
		t.Errorf("the respawned daemon has no settings record: %v", err)
	}
}

// TestEnsureLeavesADaemonHandedNoSettingsAlone: the Claude and OpenAI brokers are handed
// no settings file, and nothing here may restart them. Their background refreshers are
// not drained on SIGTERM, so a restart could cut a single-use refresh mid-flight.
func TestEnsureLeavesADaemonHandedNoSettingsAlone(t *testing.T) {
	deps, st, out := settingsFixture(t, "", `{"profile":"p"}`)
	deps.SettingsPath = ""

	EnsureSingleton(deps)

	if len(st.killed) != 0 || len(st.spawnArgv) != 0 || out.Len() != 0 {
		t.Errorf("a daemon handed no settings was restarted: killed=%v spawn=%v out=%q",
			st.killed, st.spawnArgv, out.String())
	}
}

// TestEnsureThatCannotLockReportsTheStaleDaemon: without the spawn lock the ensure may not
// kill (another launch could be mid-spawn), so it cannot replace a stale daemon — and it
// must SAY so to its caller rather than let it front the daemon as if it were current.
func TestEnsureThatCannotLockReportsTheStaleDaemon(t *testing.T) {
	deps, st, _ := settingsFixture(t, `{"profile":"old"}`, `{"profile":"new"}`)
	// A directory where the lock file goes: the OpenFile(O_WRONLY) fails with EISDIR.
	if err := os.Mkdir(deps.LockPath, 0o700); err != nil {
		t.Fatal(err)
	}

	got := EnsureSingleton(deps)

	if len(st.killed) != 0 {
		t.Errorf("killed %v without holding the spawn lock", st.killed)
	}
	if got.Stale == nil || !reflect.DeepEqual(got.Stale.Changed, []string{"profile"}) {
		t.Fatalf("Stale = %+v, want the changed key reported to the caller", got.Stale)
	}
}

// TestEnsureThatCannotLockJudgesDriftAgainstTheFrozenSnapshot: a validated singleton launch
// publishes its settings only under the flock, so when the lock fails the stable file still
// holds the previous settings. The drift must be judged against the frozen snapshot, or the
// caller fronts a daemon serving settings its config no longer says (HD-D2).
func TestEnsureThatCannotLockJudgesDriftAgainstTheFrozenSnapshot(t *testing.T) {
	deps, st, _ := settingsFixture(t, `{"profile":"old"}`, `{"profile":"old"}`)
	deps.DesiredSettings = []byte(`{"profile":"new"}`)
	deps.PublishSettings = func() error { t.Fatal("published without the lock"); return nil }
	if err := os.Mkdir(deps.LockPath, 0o700); err != nil {
		t.Fatal(err)
	}

	got := EnsureSingleton(deps)

	if len(st.killed) != 0 {
		t.Errorf("killed %v without holding the spawn lock", st.killed)
	}
	if got.Stale == nil || !reflect.DeepEqual(got.Stale.Changed, []string{"profile"}) {
		t.Fatalf("Stale = %+v, want the frozen snapshot's changed key", got.Stale)
	}
}

// TestSettingsRecordHoldsKeysNotValues: the record answers "which keys differ" and nothing
// more. It is 0600, and no value's bytes appear in it — a setting can be a credential, and
// this file sits in the machine-wide singleton directory.
func TestSettingsRecordHoldsKeysNotValues(t *testing.T) {
	deps, _, _ := settingsFixture(t, `{"profile":"a-distinctive-secret","n":7}`, `{}`)
	path := settingsRecordPath(deps)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "a-distinctive-secret") {
		t.Errorf("the record holds a value: %s", raw)
	}
	if !strings.Contains(string(raw), `"profile"`) || !strings.Contains(string(raw), `"n"`) {
		t.Errorf("the record does not name the keys: %s", raw)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("record mode = %o, want 0600", fi.Mode().Perm())
	}
}

func TestEnsurePublishesValidatedSettingsUnderSingletonLockBeforeDriftCheck(t *testing.T) {
	deps, st, _ := settingsFixture(t, `{"profile":"old"}`, `{"profile":"old"}`)
	candidate := []byte(`{"profile":"new"}` + "\n")
	deps.PublishSettings = func() error {
		probe, err := os.OpenFile(deps.LockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		defer probe.Close()
		if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			_ = syscall.Flock(int(probe.Fd()), syscall.LOCK_UN)
			return errors.New("publication callback ran without the singleton flock")
		}
		return os.WriteFile(deps.SettingsPath, candidate, 0o600)
	}

	got := EnsureSingleton(deps)
	if got.SettingsErr != nil {
		t.Fatalf("settings publication failed: %v", got.SettingsErr)
	}
	if len(st.killed) == 0 || len(st.spawnArgv) == 0 {
		t.Fatalf("validated changed settings were not applied: killed=%v spawn=%v", st.killed, st.spawnArgv)
	}
	if raw, err := os.ReadFile(deps.SettingsPath); err != nil || !bytes.Equal(raw, candidate) {
		t.Fatalf("published settings = %q, err=%v; want exact frozen candidate %q", raw, err, candidate)
	}
}

func TestConcurrentSingletonSettingsTransactionsSerializeSnapshotsWithRestart(t *testing.T) {
	st := &fakeState{alive: map[int]bool{}, reachOK: false, spawnPID: 77}
	first := newFakeDeps(t, st)
	second := first
	first.Name, second.Name = "fixture-singleton", "fixture-singleton"
	first.SettingsPath = filepath.Join(t.TempDir(), "settings.json")
	second.SettingsPath = first.SettingsPath
	first.LogPath, second.LogPath = filepath.Join(t.TempDir(), "daemon.log"), filepath.Join(t.TempDir(), "daemon.log")
	entered := make(chan string, 2)
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(release)
	var mu sync.Mutex
	var spawned [][]byte
	first.PublishSettings = func() error {
		entered <- "first"
		<-releaseFirst
		return os.WriteFile(first.SettingsPath, []byte(`{"profile":"first-frozen"}`+"\n"), 0o600)
	}
	second.PublishSettings = func() error {
		entered <- "second"
		return os.WriteFile(second.SettingsPath, []byte(`{"profile":"second-frozen"}`+"\n"), 0o600)
	}
	spawn := func(_ []string, _ string) (int, func() bool, error) {
		data, err := os.ReadFile(first.SettingsPath)
		if err != nil {
			return 0, nil, err
		}
		st.mu.Lock()
		st.alive[77] = true
		st.reachOK = true
		st.mu.Unlock()
		mu.Lock()
		spawned = append(spawned, append([]byte(nil), data...))
		mu.Unlock()
		if err := os.WriteFile(first.SocketPath, nil, 0o600); err != nil {
			return 0, nil, err
		}
		return 77, func() bool { return false }, nil
	}
	first.Spawn, second.Spawn = spawn, spawn
	first.Kill = func(pid int, _ syscall.Signal) error {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.alive[pid] = false
		return nil
	}
	second.Kill = first.Kill
	firstDone, secondDone := make(chan Ensured, 1), make(chan Ensured, 1)
	firstReceived, secondReceived := false, false
	t.Cleanup(func() {
		release()
		if !firstReceived {
			select {
			case <-firstDone:
			case <-time.After(5 * time.Second):
				t.Error("first singleton ensure did not finish during cleanup")
			}
		}
		if !secondReceived {
			select {
			case <-secondDone:
			case <-time.After(5 * time.Second):
				t.Error("second singleton ensure did not finish during cleanup")
			}
		}
	})
	go func() { firstDone <- EnsureSingleton(first) }()
	if got := <-entered; got != "first" {
		t.Fatalf("first publisher entered as %q", got)
	}
	go func() { secondDone <- EnsureSingleton(second) }()
	select {
	case got := <-entered:
		t.Fatalf("second publication %q interleaved while the first transaction held the flock", got)
	case <-time.After(75 * time.Millisecond):
	}
	release()
	firstResult := <-firstDone
	firstReceived = true
	if got := <-entered; got != "second" {
		t.Fatalf("second publisher entered as %q after first transaction", got)
	}
	secondResult := <-secondDone
	secondReceived = true
	if firstResult.SettingsErr != nil || !firstResult.Started {
		t.Fatalf("first transaction result = %+v", firstResult)
	}
	if secondResult.SettingsErr != nil || !secondResult.Started {
		t.Fatalf("second transaction result = %+v", secondResult)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(spawned) != 2 || !bytes.Contains(spawned[0], []byte("first-frozen")) ||
		!bytes.Contains(spawned[1], []byte("second-frozen")) {
		t.Fatalf("spawned settings snapshots = %q, want serialized first then second", spawned)
	}
}

func TestEnsurePublicationFailureLeavesCurrentSingletonAndSettingsUntouched(t *testing.T) {
	deps, st, _ := settingsFixture(t, `{"profile":"current"}`, `{"profile":"current"}`)
	before, err := os.ReadFile(deps.SettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	deps.PublishSettings = func() error { return errors.New("fixture publication failure") }

	got := EnsureSingleton(deps)
	if got.SettingsErr == nil {
		t.Fatal("publication error was not returned to the caller")
	}
	if len(st.killed) != 0 || len(st.spawnArgv) != 0 {
		t.Fatalf("failed candidate changed singleton lifecycle: killed=%v spawn=%v", st.killed, st.spawnArgv)
	}
	after, err := os.ReadFile(deps.SettingsPath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("settings changed after failed publication: before=%q after=%q err=%v", before, after, err)
	}
}

// TestCompareSettingsNamesEveryKindOfDifference: a changed value, a key the config added,
// and a key it dropped all make a daemon stale, and all three are named.
func TestCompareSettingsNamesEveryKindOfDifference(t *testing.T) {
	deps, _, _ := settingsFixture(t, `{"same":1,"changed":"x","dropped":true}`, `{}`)
	drift, ok := CompareSettings(deps, []byte(`{"same":1,"changed":"y","added":[]}`))
	if !ok {
		t.Fatal("a flat JSON object was not judged")
	}
	if want := []string{"added", "changed", "dropped"}; !reflect.DeepEqual(drift.Changed, want) {
		t.Errorf("Changed = %v, want %v", drift.Changed, want)
	}
	if _, ok := CompareSettings(deps, []byte(`not json`)); ok {
		t.Error("an unparseable payload was judged")
	}
}

// TestBrokerKillRemovesTheSettingsRecord: the record describes the process the PID file
// names, so it goes when that process does — or a later daemon would inherit it.
func TestBrokerKillRemovesTheSettingsRecord(t *testing.T) {
	deps, _, _ := settingsFixture(t, `{"profile":"p"}`, `{"profile":"p"}`)
	BrokerKill(deps, syscall.SIGTERM, 0)
	if _, err := os.Stat(settingsRecordPath(deps)); !os.IsNotExist(err) {
		t.Errorf("settings record survived BrokerKill: %v", err)
	}
}

// TestSingletonDepsDerivesTheSettingsPathFromTheArgv: the settings file is a function of
// the loophole name, and the argv decides only whether the daemon is handed it. This is
// the half that makes the mechanism generic — no daemon is named anywhere.
func TestSingletonDepsDerivesTheSettingsPathFromTheArgv(t *testing.T) {
	root := t.TempDir()
	real := loopholes.StateDirFor
	loopholes.StateDirFor = func(name string) string { return filepath.Join(root, name) }
	t.Cleanup(func() { loopholes.StateDirFor = real })

	settings := loopholes.SettingsFileFor("yjtest-x")
	if got := SingletonDeps("yjtest-x", []string{"d", "--settings", settings}).SettingsPath; got != settings {
		t.Errorf("SettingsPath = %q, want %q", got, settings)
	}
	if got := SingletonDeps("yjtest-x", []string{"d", "--settings=" + settings}).SettingsPath; got != settings {
		t.Errorf("SettingsPath for the --flag=value spelling = %q, want %q", got, settings)
	}
	if got := SingletonDeps("yjtest-x", []string{"d", "--socket", "/s"}).SettingsPath; got != "" {
		t.Errorf("SettingsPath = %q for an argv handing the daemon no settings, want empty", got)
	}
}

// A preparation failure aborts the singleton settings transaction before it publishes: the
// live daemon, its settings record and the stable settings file all stay as they were, so a
// candidate the launch could not prepare is never left standing as the configured snapshot.
func TestEnsurePreparationFailureLeavesLiveSingletonAndSettingsUntouched(t *testing.T) {
	deps, st, _ := settingsFixture(t, `{"profile":"current"}`, `{"profile":"current"}`)
	before, err := os.ReadFile(deps.SettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	published := false
	deps.PublishSettings = func() error {
		published = true
		return os.WriteFile(deps.SettingsPath, []byte(`{"profile":"candidate"}`+"\n"), 0o600)
	}
	deps.PrepareLocked = func() (func() error, error) { return nil, errors.New("fixture preparation failure") }

	got := EnsureSingleton(deps)
	if got.Outcome.Kind != hostservice.StartupKindPreparationFailed || got.Started {
		t.Fatalf("outcome = %s started=%v, want preparation-failed and nothing started", got.Outcome.Kind, got.Started)
	}
	if published {
		t.Fatal("the candidate snapshot was published although its preparation failed")
	}
	if len(st.killed) != 0 || len(st.spawnArgv) != 0 {
		t.Fatalf("failed preparation changed the singleton lifecycle: killed=%v spawn=%v", st.killed, st.spawnArgv)
	}
	if after, err := os.ReadFile(deps.SettingsPath); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("stable settings changed after failed preparation: before=%q after=%q err=%v", before, after, err)
	}
	if drift, ok := RunningSettingsDrift(deps); !ok || drift.Stale() {
		t.Fatalf("live daemon's settings record no longer matches its settings: drift=%+v ok=%v", drift, ok)
	}
	if pid, _ := BrokerReadPID(deps); pid != 42 {
		t.Fatalf("pid file names %d, want the untouched live daemon 42", pid)
	}
}

// A migration that fails after preparation stopped the old daemon still published the validated
// snapshot BEFORE that stop (the transaction's publication precedes any stop), and stamps no
// record for a daemon that was never started.
func TestEnsureMigrationFailurePublishesBeforeStopAndStartsNothing(t *testing.T) {
	deps, st, _ := settingsFixture(t, `{"profile":"current"}`, `{"profile":"current"}`)
	candidate := []byte(`{"profile":"candidate"}` + "\n")
	var order []string
	deps.PublishSettings = func() error {
		order = append(order, "publish")
		return os.WriteFile(deps.SettingsPath, candidate, 0o600)
	}
	kill := deps.Kill
	deps.Kill = func(pid int, sig syscall.Signal) error {
		order = append(order, "kill")
		return kill(pid, sig)
	}
	deps.PrepareLocked = func() (func() error, error) {
		order = append(order, "prepare")
		return func() error { return errors.New("fixture migration failure") }, nil
	}

	got := EnsureSingleton(deps)
	if got.Outcome.Kind != hostservice.StartupKindMigrationFailed || got.Started || len(st.spawnArgv) != 0 {
		t.Fatalf("outcome = %s started=%v spawn=%v, want migration-failed and nothing started",
			got.Outcome.Kind, got.Started, st.spawnArgv)
	}
	if len(order) < 3 || order[0] != "prepare" || order[1] != "publish" || order[2] != "kill" {
		t.Fatalf("transaction order = %v, want prepare, publish, then the migration's stop", order)
	}
	if _, err := os.Stat(settingsRecordPath(deps)); !os.IsNotExist(err) {
		t.Fatalf("a settings record survived for a daemon that is no longer running: %v", err)
	}
}
