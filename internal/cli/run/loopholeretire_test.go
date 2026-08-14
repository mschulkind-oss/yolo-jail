package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// retireHome points HOME at a temp dir and returns it, so the state root, the log dir, and
// the ownership record all live under the test's own tree.
func retireHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// writeLoopholeState lays down a per-loophole state dir with one secret in it, plus the
// daemon's host-service log, as a launch that actually ran the loophole would leave them.
func writeLoopholeState(t *testing.T, loophole, secret string) (stateDir, logPath string) {
	t.Helper()
	stateDir = loopholes.StateDirFor(loophole)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "ca.key"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := loopholeLogDir()
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath = filepath.Join(logDir, "host-service-"+loophole+".log")
	if err := os.WriteFile(logPath, []byte("started\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return stateDir, logPath
}

func retireOptions(t *testing.T, out *bytes.Buffer) *Options {
	t.Helper()
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout = out
	o.Stderr = discardBuf()
	o.Now = func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) }
	return o
}

// THE STATE ROOT MUST BE THE ONE loopholes.StateDirFor USES. This file derives the parent of
// a per-loophole state dir; StateDirFor derives the dir. A divergence is a retirement that
// archives nothing while reporting success — so it is pinned rather than commented.
func TestLoopholeStateRootMatchesStateDirFor(t *testing.T) {
	retireHome(t)
	if got, want := filepath.Dir(loopholes.StateDirFor("acme-proxy")), loopholeStateRoot(); got != want {
		t.Errorf("state root = %q but StateDirFor's parent is %q — retirement would look in "+
			"the wrong tree and report nothing", want, got)
	}
}

// The log dir must be the one loopholesruntime.go writes host-service-<name>.log into, for
// the same reason.
func TestLoopholeLogDirMatchesTheSpawnPath(t *testing.T) {
	retireHome(t)
	if got, want := loopholeLogDir(), filepath.Join(paths.GlobalStorage(), "logs"); got != want {
		t.Errorf("log dir = %q, want %q", got, want)
	}
}

// A LOOPHOLE THIS LAUNCH SHIPS IS RECORDED. Nothing on disk records which pack owns a
// name-keyed state dir (§4.5's measured gap), so without this write the retirement has no
// evidence to act on at all.
func TestStagingRecordsLoopholeOwnership(t *testing.T) {
	retireHome(t)
	writeUserPacks(t, os.Getenv("HOME"), `[]`)
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy"}`)

	var out bytes.Buffer
	retireOptions(t, &out).recordAndRetirePackLoopholes([]*packload.Pack{p})

	rec, err := packstage.LoadLoopholeOwners(packLoopholeOwnersPath())
	if err != nil {
		t.Fatal(err)
	}
	if rec.Owners["acme-proxy"] != "acme" {
		t.Errorf("ownership record = %v, want acme-proxy owned by acme", rec.Owners)
	}
}

// THE DETECTOR, end to end on the launch path: a pack that leaves `packs` has its loophole's
// state ARCHIVED (not deleted), the record forgets it, and the user is told where the copy
// went.
//
// This is the case that motivates the whole item: for a pack-shipped INTERCEPTING loophole
// the state dir holds a CA PRIVATE KEY, and before this it was left on the machine forever
// with nothing on disk able to say whose it was.
func TestDepartedPackLoopholeStateIsArchivedOnTheLaunchPath(t *testing.T) {
	home := retireHome(t)
	// The pack IS configured for the first pass, so the record is written.
	pack := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy"}`)
	writeUserPacks(t, home, `[{"name": "acme", "source": "file://`+pack.Root+`"}]`)
	var out bytes.Buffer
	retireOptions(t, &out).recordAndRetirePackLoopholes([]*packload.Pack{pack})

	stateDir, logPath := writeLoopholeState(t, "acme-proxy", "PRIVATE KEY")

	// Now the user drops it from `packs`, and the next launch stages nothing.
	writeUserPacks(t, home, `[]`)
	out.Reset()
	retireOptions(t, &out).recordAndRetirePackLoopholes(nil)

	if _, err := os.Lstat(stateDir); !os.IsNotExist(err) {
		t.Error("the departed loophole's state dir is still live")
	}
	if _, err := os.Lstat(logPath); !os.IsNotExist(err) {
		t.Error("the departed loophole's host-service log is still live")
	}
	// ARCHIVED, and recoverable: being wrong about ownership must cost one `mv` back.
	gen := filepath.Join(loopholeStateRoot(), packstage.RetiredLoopholeStateDir,
		packstage.ArchiveStamp(time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)), "acme-proxy")
	data, err := os.ReadFile(filepath.Join(gen, "state", "ca.key"))
	if err != nil || string(data) != "PRIVATE KEY" {
		t.Fatalf("the CA key is not recoverable from the archive: %q %v", data, err)
	}
	// NO SILENT CAPS: the user is told, by name, that it was archived and WHERE. An archive
	// the user cannot find is a deletion from their point of view.
	got := out.String()
	for _, want := range []string{"acme", "acme-proxy", "ARCHIVED", gen} {
		if !strings.Contains(got, want) {
			t.Errorf("the retirement notice does not mention %q:\n%s", want, got)
		}
	}
	// The record forgets it, so a second launch does not report the same retirement again.
	rec, _ := packstage.LoadLoopholeOwners(packLoopholeOwnersPath())
	if _, still := rec.Owners["acme-proxy"]; still {
		t.Errorf("the record still names the retired loophole: %v", rec.Owners)
	}
	out.Reset()
	retireOptions(t, &out).recordAndRetirePackLoopholes(nil)
	if strings.Contains(out.String(), "ARCHIVED") {
		t.Errorf("the retirement repeated on the next launch:\n%s", out.String())
	}
}

// A pack STILL IN THE CONFIG keeps its loophole state, even when it failed to resolve this
// launch and so is absent from the loaded set. That is the case that rules out comparing
// against the resolved packs: an offline launch would otherwise archive a CA every time, and
// unlike a briefing (re-rendered the moment the remote is reachable) an archived key does not
// come back until the user goes digging.
func TestStillConfiguredPackKeepsItsLoopholeStateWhenUnresolvable(t *testing.T) {
	home := retireHome(t)
	pack := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy"}`)
	writeUserPacks(t, home, `[{"name": "acme", "source": "file://`+pack.Root+`"}]`)
	var out bytes.Buffer
	retireOptions(t, &out).recordAndRetirePackLoopholes([]*packload.Pack{pack})
	stateDir, _ := writeLoopholeState(t, "acme-proxy", "PRIVATE KEY")

	// Config unchanged; the pack resolved to nothing this launch (empty loaded set).
	out.Reset()
	retireOptions(t, &out).recordAndRetirePackLoopholes(nil)

	if _, err := os.ReadFile(filepath.Join(stateDir, "ca.key")); err != nil {
		t.Errorf("a still-configured pack's CA was archived on an unresolvable launch: %v", err)
	}
	if strings.Contains(out.String(), "ARCHIVED") {
		t.Errorf("an offline launch reported a retirement:\n%s", out.String())
	}
}

// REFUSES ON AN UNKNOWN CONFIGURED SET. A malformed `packs` list means "the user configured
// packs and yolo cannot tell which"; reading that as "no packs" would archive every
// loophole's state on the machine at once. Same guard pruneDroppedPackOutput opens with, and
// here it protects a private key.
func TestStagePacksRefusesRetirementWithUnknownPackSet(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"unparseable config", `{"packs": `},
		{"not a list", `{"packs": {"a": "b"}}`},
		{"every entry invalid", `{"packs": ["nonsense"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := retireHome(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			// A record naming a pack the (broken) config does not list.
			rec := &packstage.LoopholeOwners{Owners: map[string]string{"acme-proxy": "acme"}}
			if err := rec.Save(packLoopholeOwnersPath()); err != nil {
				t.Fatal(err)
			}
			stateDir, _ := writeLoopholeState(t, "acme-proxy", "PRIVATE KEY")

			var out bytes.Buffer
			retireOptions(t, &out).recordAndRetirePackLoopholes(nil)

			if _, err := os.ReadFile(filepath.Join(stateDir, "ca.key")); err != nil {
				t.Errorf("a broken `packs` list authorized archiving a CA: %v", err)
			}
		})
	}
}

// A CORRUPT ownership record retires nothing AND is not overwritten. Overwriting it would
// silently turn an unreadable record into an empty one, orphaning every existing state dir
// unattributed forever.
func TestCorruptOwnershipRecordRetiresNothingAndIsKept(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	path := packLoopholeOwnersPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir, _ := writeLoopholeState(t, "acme-proxy", "PRIVATE KEY")

	var out bytes.Buffer
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy"}`)
	retireOptions(t, &out).recordAndRetirePackLoopholes([]*packload.Pack{p})

	if _, err := os.ReadFile(filepath.Join(stateDir, "ca.key")); err != nil {
		t.Errorf("a corrupt record authorized archiving a CA: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{not json" {
		t.Errorf("the corrupt record was overwritten (%q, %v); an unreadable record must not "+
			"silently become an empty one", raw, err)
	}
	if !strings.Contains(out.String(), "ownership record") {
		t.Errorf("the corrupt record was not reported:\n%s", out.String())
	}
}

// A loophole that NEVER RAN has no state on disk: it is forgotten silently rather than
// producing a line about a directory that never existed. NO SILENT CAPS is about things the
// user loses.
func TestDepartedLoopholeWithNoStateIsSilent(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	rec := &packstage.LoopholeOwners{Owners: map[string]string{"never-ran": "ghost"}}
	if err := rec.Save(packLoopholeOwnersPath()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	retireOptions(t, &out).recordAndRetirePackLoopholes(nil)
	if strings.Contains(out.String(), "ARCHIVED") {
		t.Errorf("a loophole with no state produced a retirement notice:\n%s", out.String())
	}
	got, _ := packstage.LoadLoopholeOwners(packLoopholeOwnersPath())
	if _, still := got.Owners["never-ran"]; still {
		t.Errorf("a departed loophole with no state was not forgotten: %v", got.Owners)
	}
}

// RETIRE BEFORE RECORD. A pack can be dropped and a different pack shipping the SAME loophole
// name selected in one edit; recording first would rewrite the owner, make the departure
// invisible, and hand the new pack the old pack's CA.
func TestRetirementPrecedesRecordingSoAReusedNameCannotInheritState(t *testing.T) {
	home := retireHome(t)
	old := writeLoopholePack(t, "old-pack", "acme-proxy", `{"name": "acme-proxy"}`)
	writeUserPacks(t, home, `[{"name": "old-pack", "source": "file://`+old.Root+`"}]`)
	var out bytes.Buffer
	retireOptions(t, &out).recordAndRetirePackLoopholes([]*packload.Pack{old})
	stateDir, _ := writeLoopholeState(t, "acme-proxy", "OLD PACKS KEY")

	// One edit: old-pack out, new-pack in, same loophole name.
	newPack := writeLoopholePack(t, "new-pack", "acme-proxy", `{"name": "acme-proxy"}`)
	writeUserPacks(t, home, `[{"name": "new-pack", "source": "file://`+newPack.Root+`"}]`)
	out.Reset()
	retireOptions(t, &out).recordAndRetirePackLoopholes([]*packload.Pack{newPack})

	if _, err := os.Lstat(stateDir); !os.IsNotExist(err) {
		t.Error("the new pack inherited the departed pack's state dir — the old pack's CA " +
			"private key would be trusted under a name the user now associates with a " +
			"different author")
	}
	if !strings.Contains(out.String(), "old-pack") {
		t.Errorf("the retirement does not attribute the archive to the departed pack:\n%s",
			out.String())
	}
	rec, _ := packstage.LoadLoopholeOwners(packLoopholeOwnersPath())
	if rec.Owners["acme-proxy"] != "new-pack" {
		t.Errorf("the record did not re-own the name after the retirement: %v", rec.Owners)
	}
}

// RETIREMENT IS NEVER FATAL. Staging is fail-closed (A12) because a missing pack changes what
// the jail contains; this is bookkeeping over the host state dir, and trading a working jail
// for a tidy record is the wrong direction. Driven through stageRunPacks, which is the real
// caller and the thing that must not start returning false.
func TestRetirementFailureDoesNotFailTheLaunch(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	// Make the record path unwritable by putting a DIRECTORY where the file goes.
	if err := os.MkdirAll(packLoopholeOwnersPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	o := retireOptions(t, &out)
	if _, ok := o.stageRunPacks("yolo-retire-test"); !ok {
		t.Errorf("an unwritable ownership record failed the launch:\n%s", out.String())
	}
}

// THE WIRING: the retirement pass runs from stageRunPacks — the launch path, on every
// invocation including attach. A pass nothing calls is the shape of defect this whole item
// exists to fix, so it is asserted from the real entry point rather than from the function.
//
// Driven through the RETIREMENT direction rather than the recording one, deliberately.
// Recording requires a pack whose staged pack.json declares `{"kind": "loophole"}`, and that
// kind is not decodable in this build yet (it lands with item 5) — so a recording-side
// fixture would have to fail staging to reach the pass, which asserts nothing about the
// wiring. A pre-existing record plus a config that no longer names its pack exercises the
// same call site with inputs that exist today, and it is the direction where being unwired
// costs the user something (a private key left behind).
func TestStageRunPacksRunsTheRetirementPass(t *testing.T) {
	home := retireHome(t)
	writeUserPacks(t, home, `[]`)
	rec := &packstage.LoopholeOwners{Owners: map[string]string{"acme-proxy": "acme"}}
	if err := rec.Save(packLoopholeOwnersPath()); err != nil {
		t.Fatal(err)
	}
	stateDir, _ := writeLoopholeState(t, "acme-proxy", "PRIVATE KEY")

	var out bytes.Buffer
	o := retireOptions(t, &out)
	if _, ok := o.stageRunPacks("yolo-retire-wiring"); !ok {
		t.Fatalf("stageRunPacks failed:\n%s", out.String())
	}
	if _, err := os.Lstat(stateDir); !os.IsNotExist(err) {
		t.Errorf("stageRunPacks left the departed loophole's state live — the retirement pass "+
			"is not wired into the launch path:\n%s", out.String())
	}
	got, _ := packstage.LoadLoopholeOwners(packLoopholeOwnersPath())
	if _, still := got.Owners["acme-proxy"]; still {
		t.Errorf("the record still names the retired loophole after a launch: %v", got.Owners)
	}
}

// THE RECORDING SIDE IS INERT UNTIL THE `loophole` KIND DECODES, and that is a fact worth
// pinning rather than discovering: packload.LoadDir is STRICT on the launch path, so a
// pack.json declaring an unknown kind is a load PROBLEM and stagePacks is fail-closed (A12).
// So no staged pack can carry a loophole contribution in this build.
//
// What this asserts is that the recording pass is nevertheless CORRECT the moment the kind
// lands — it matches the contribution by VALUE, so nothing here needs editing — and that the
// gap is the loader's, not this file's.
func TestLoopholeContributionIsNotDecodableYet(t *testing.T) {
	dir := t.TempDir()
	body := `{"contributes":[{"kind":"` + packLoopholeKindName + `","from":"loopholes/acme-proxy"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, probs := packload.LoadDir(dir, "acme", true)
	if len(probs) == 0 {
		// The kind has landed. The recording pass then works through the real loader, and this
		// test's reason for existing is gone — say so rather than passing quietly.
		t.Skip("the `loophole` kind now decodes; recording works through the real loader")
	}
	joined := strings.Join(probs, "\n")
	if !strings.Contains(joined, packLoopholeKindName) {
		t.Errorf("the refusal does not name the kind, so this test is pinning the wrong "+
			"thing:\n%s", joined)
	}
}

// THE PROCESS-GROUP KILL IS STILL THERE (§4.5's accepted half, verified rather than
// re-implemented). It is the only revocation yolo has, and it is what makes the sentence
// "selection controls activation, not revocation" a bounded claim rather than an excuse: the
// daemon's own group dies at teardown, and only what it managed to persist outside that group
// survives.
//
// Asserted over the SOURCE because the behaviour needs a real spawned process tree to observe,
// and a signal to a negative pid is what the assertion is about — a unit test that spawned one
// would be racing the kernel to check it.
func TestTeardownStillKillsTheProcessGroup(t *testing.T) {
	data, err := os.ReadFile("loopholesruntime.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{
		"Setsid: true",                         // the spawn makes the child a group leader
		"syscall.Kill(-pgid, syscall.SIGTERM)", // teardown signals the GROUP
		"syscall.Kill(-pgid, syscall.SIGKILL)", // and the straggler kill does too
	} {
		if !strings.Contains(src, want) {
			t.Errorf("loopholesruntime.go no longer contains %q — signalling only the direct "+
				"child leaves anything the daemon forked running after deselection "+
				"(loophole-packaging.md §4.5 accepted exactly this fix)", want)
		}
	}
}

// writeLoopholePack writes a pack tree with one `loophole` contribution pointing at a module
// dir holding the given manifest body, and returns a loaded Pack for it.
func writeLoopholePack(t *testing.T, packName, loopholeName, manifestBody string) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	mod := filepath.Join(root, "loopholes", loopholeName)
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifestBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{
		Name: packName, Root: root, MayAccessHost: true,
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
			{Kind: packdecl.Kind(packLoopholeKindName), From: "loopholes/" + loopholeName},
		}},
	}
}
