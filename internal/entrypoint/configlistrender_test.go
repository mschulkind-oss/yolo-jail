package entrypoint

// configlistrender_test.go is the BEHAVIORAL proof that `config-list` is wired at both render
// boundaries (docs/design/additive-config-lists.md, OQ-AL1 and OQ-AL2): the jail boot
// (ConfigurePackSurfaces, the loop the entrypoint runs) and the host apply (RenderHostPack,
// driven the way `yolo host apply` drives it — collect across the set, render each pack).
// Every test renders and then reads the FILE the agent would read; an in-jail edit is
// simulated by editing that file between renders. Every home is a t.TempDir().

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

const (
	listKilo      = "git:github.com/mschulkind/kilo-pi-provider"
	listSettings  = ".pi/agent/settings.json"
	listInstalled = "npm:pi-installed"
)

// listOwnerPack declares pi/settings with an owner package list, in the given mode; extra
// fields are merged into the surface declaration (managed, a different codec…).
func listOwnerPack(t *testing.T, mode string, extra map[string]any) *packload.Pack {
	t.Helper()
	surface := map[string]any{
		"agent": "pi", "name": "settings", "codec": "json", "path": "~/" + listSettings,
		"defaults": map[string]any{"packages": []any{"npm:owner-a", "npm:owner-b"}},
	}
	if mode != "" {
		surface["mode"] = mode
	}
	for k, v := range extra {
		surface[k] = v
	}
	raw, err := json.Marshal([]any{surface})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "pi", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: raw}},
	}}
}

// listContributorPack appends entries to a list another pack owns — the personal pack.
func listContributorPack(t *testing.T, name, target, path string, add ...any) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal(add)
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigList, Surface: target, Path: path, Add: raw},
		},
	}}
}

func personalPack(t *testing.T) *packload.Pack {
	return listContributorPack(t, "personal", "pi/settings", "/packages", listKilo, "npm:owner-a")
}

func bootJail(t *testing.T, e *Env, packs ...*packload.Pack) {
	t.Helper()
	ConfigurePackSurfaces(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot failed: %v", fails)
	}
}

func packagesAt(t *testing.T, home string) []any {
	t.Helper()
	got, _ := readRenderedJSON(t, home, listSettings)["packages"].([]any)
	return got
}

// editSettings changes the rendered file the way an agent (`pi install`) would.
func editSettings(t *testing.T, home string, fn func(m map[string]any)) {
	t.Helper()
	path := filepath.Join(home, listSettings)
	m := readRenderedJSON(t, home, listSettings)
	fn(m)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendInstalled(m map[string]any) {
	m["packages"] = append(m["packages"].([]any), listInstalled)
}

func removeEntry(entry string) func(m map[string]any) {
	return func(m map[string]any) {
		var kept []any
		for _, e := range m["packages"].([]any) {
			if e != entry {
				kept = append(kept, e)
			}
		}
		m["packages"] = kept
	}
}

func wantPackages(t *testing.T, home string, want ...any) {
	t.Helper()
	if got := packagesAt(t, home); !reflect.DeepEqual(got, want) {
		t.Fatalf("packages = %#v\nwant       %#v", got, want)
	}
}

func readSidecar(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// ── the jail boundary ───────────────────────────────────────────────────────────────────

// The pack list selected: pi receives the owner's entries, then the added ones, once each,
// in stable order — and the boot names who appended (rule 5).
func TestJailConfigListAppendsAfterTheOwnersEntries(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	bootJail(t, e, listOwnerPack(t, "", nil), personalPack(t))
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b", listKilo)
	if !strings.Contains(errw.String(), "pi/settings: config-list entries from personal") {
		t.Errorf("the boot did not name the contributing pack:\n%s", errw.String())
	}
	// Without the contribution the owner's list is exactly what renders.
	e2, _ := overlayRenderEnv(t)
	bootJail(t, e2, listOwnerPack(t, "", nil))
	wantPackages(t, e2.Home, "npm:owner-a", "npm:owner-b")
	if _, err := os.Stat(prismListCapturePath(e2, "pi", "settings")); !os.IsNotExist(err) {
		t.Errorf("a surface with no config-list grew a list-capture sidecar (err=%v)", err)
	}
}

// THE MOTIVATING CASE. The agent appends (`pi install`): the entry is kept as the user's and
// the pack's entries are NOT captured; the pack is then dropped and exactly its entry leaves.
func TestJailConfigListAgentAppendKeptAndPackDropRemovesItsEntries(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	owner := listOwnerPack(t, "", nil)
	bootJail(t, e, owner, personalPack(t))
	editSettings(t, e.Home, appendInstalled)
	bootJail(t, e, owner, personalPack(t))
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b", listKilo, listInstalled)

	if overlay := readSidecar(t, prismOverlayPath(e, "pi", "settings")); strings.Contains(overlay, "packages") {
		t.Fatalf("the whole array was captured into the overlay (the freeze OQ-AL1 rules out):\n%s", overlay)
	}
	capture := readSidecar(t, prismListCapturePath(e, "pi", "settings"))
	if !strings.Contains(capture, listInstalled) || strings.Contains(capture, listKilo) {
		t.Fatalf("list capture = %s — want the agent's entry and not the pack's", capture)
	}
	if !strings.Contains(errw.String(), "1 list entry from captured in-jail edits") {
		t.Errorf("the boot did not announce the captured list entry:\n%s", errw.String())
	}

	bootJail(t, e, owner) // the personal pack is dropped
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b", listInstalled)
}

// The user deletes a pack-added entry: a per-entry removal the capture holds, across renders
// and across the pack being dropped and re-added.
func TestJailConfigListUserDeletionStaysDeleted(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	owner := listOwnerPack(t, "", nil)
	bootJail(t, e, owner, personalPack(t))
	editSettings(t, e.Home, removeEntry(listKilo))
	bootJail(t, e, owner, personalPack(t))
	bootJail(t, e, owner, personalPack(t))
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b")
	bootJail(t, e, owner)
	bootJail(t, e, owner, personalPack(t))
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b")
}

// OQ-AL2: an ordinary overlay replaces the array, and a later list contribution re-adds an
// entry the replacement dropped.
func TestJailConfigListReAddsAfterAnOverlayReplacement(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	replacer := &packload.Pack{Name: "replacer", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfigOverlay, Surface: "pi/settings",
			Raw: json.RawMessage(`{"managed":{"packages":["npm:only"]}}`)}},
	}}
	bootJail(t, e, listOwnerPack(t, "", nil), replacer, personalPack(t))
	wantPackages(t, e.Home, "npm:only", listKilo, "npm:owner-a")
}

// A managed replacement still wins over the assembled list, and so does a captured
// whole-value edit (deleting the key) — which the boot names, with the reset that undoes it.
func TestJailConfigListHigherLayersStillWin(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	bootJail(t, e, listOwnerPack(t, "", map[string]any{"managed": map[string]any{"packages": []any{"pinned"}}}),
		personalPack(t))
	wantPackages(t, e.Home, "pinned")

	e2, errw := overlayRenderEnv(t)
	owner := listOwnerPack(t, "", nil)
	bootJail(t, e2, owner, personalPack(t))
	editSettings(t, e2.Home, func(m map[string]any) { delete(m, "packages") })
	bootJail(t, e2, owner, personalPack(t))
	if got := packagesAt(t, e2.Home); got != nil {
		t.Fatalf("a captured deletion lost to the list: %#v", got)
	}
	if !strings.Contains(errw.String(), "yolo config reset pi/settings") {
		t.Errorf("the masked list was not reported:\n%s", errw.String())
	}
}

// MIGRATION: a whole array the old capture froze at the path converts on the first boot that
// sees the path as a list path. Stated result: B's order (the owner's list minus what the
// user removed), then the contributions, then the user's additions; the overlay no longer
// holds the array and the record carries add [mine] / remove [owner-b].
func TestJailConfigListMigratesALegacyWholeArrayCapture(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	legacy := `{"packages":["mine","npm:owner-a"]}` + "\n"
	sidecars := prismSidecarDir(e)
	if err := os.MkdirAll(sidecars, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		prismOverlayPath(e, "pi", "settings"):                   legacy,
		prismLastRenderPath(e, "pi", "settings"):                legacy,
		filepath.Join(e.Home, filepath.FromSlash(listSettings)): legacy,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bootJail(t, e, listOwnerPack(t, "", nil), personalPack(t))
	wantPackages(t, e.Home, "npm:owner-a", listKilo, "mine")
	if overlay := readSidecar(t, prismOverlayPath(e, "pi", "settings")); strings.Contains(overlay, "packages") {
		t.Fatalf("the legacy array survived the migration:\n%s", overlay)
	}
	var recs map[string]map[string][]any
	if err := json.Unmarshal([]byte(readSidecar(t, prismListCapturePath(e, "pi", "settings"))), &recs); err != nil {
		t.Fatal(err)
	}
	if got := recs["/packages"]; !reflect.DeepEqual(got["add"], []any{"mine"}) ||
		!reflect.DeepEqual(got["remove"], []any{"npm:owner-b"}) {
		t.Fatalf("converted record = %#v", got)
	}
	if !strings.Contains(errw.String(), "converted the whole-array capture at /packages") {
		t.Errorf("the conversion was not announced:\n%s", errw.String())
	}
}

// THE LAUNCH REFUSAL (OQ-AL1): a list on a path that cannot capture per entry — a keyless
// surface, whose capture is the whole file — refuses the boot, naming the surface and mode.
func TestJailConfigListOnAPathThatCannotCapturePerEntryRefusesTheBoot(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	owner := listOwnerPack(t, "", map[string]any{"name": "hosts", "codec": "lines",
		"path": "~/.pi/hosts", "defaults": nil})
	ConfigurePackSurfaces(e, []*packload.Pack{owner,
		listContributorPack(t, "personal", "pi/hosts", "/x", "b")})
	fails := strings.Join(e.GenFailures(), "\n")
	for _, want := range []string{"config-list on pi/hosts", "mode stateful", "per entry"} {
		if !strings.Contains(fails, want) {
			t.Errorf("boot failures %q do not name %q", fails, want)
		}
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".pi", "hosts")); !os.IsNotExist(err) {
		t.Errorf("the refused surface was written anyway (err=%v)", err)
	}
}

// RULE 3 at the boot: a non-array at the path refuses the surface's render, fatally.
func TestJailConfigListTypeConflictIsFatal(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	ConfigurePackSurfaces(e, []*packload.Pack{
		listOwnerPack(t, "", map[string]any{"defaults": map[string]any{"packages": "npm:a"}}),
		personalPack(t)})
	fails := strings.Join(e.GenFailures(), "\n")
	if !strings.Contains(fails, "/packages") || !strings.Contains(fails, "personal") {
		t.Fatalf("boot failures = %q, want the path and the pack named", fails)
	}
}

// `computed`: the list is a pure function of its inputs; a drop removes the entries.
func TestJailConfigListOnAComputedSurface(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	owner := listOwnerPack(t, "computed", nil)
	bootJail(t, e, owner, personalPack(t))
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b", listKilo)
	bootJail(t, e, owner)
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b")
}

// `rmw` in a jail: the insert record lives in the workspace store, and a drop removes only
// what yolo inserted.
func TestJailConfigListOnAnRMWSurface(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	owner := listOwnerPack(t, "rmw", nil)
	bootJail(t, e, owner, personalPack(t))
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b", listKilo)
	if rec := readSidecar(t, e.renderTarget().ListRecordPath("pi", "settings")); !strings.Contains(rec, listKilo) {
		t.Fatalf("insert record = %s", rec)
	}
	editSettings(t, e.Home, appendInstalled)
	bootJail(t, e, owner)
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b", listInstalled)
}

// R2: an ownerless list is inert and named; an `unrendered` target is inert and named.
func TestJailConfigListOrphanAndUnrenderedAreInertAndNamed(t *testing.T) {
	e, errw := overlayRenderEnv(t)
	bootJail(t, e, personalPack(t))
	if !strings.Contains(errw.String(), "config-list  no effect — pi/settings has no owner") {
		t.Errorf("orphan not reported:\n%s", errw.String())
	}
	e2, errw2 := overlayRenderEnv(t)
	bootJail(t, e2, listOwnerPack(t, "unrendered", nil), personalPack(t))
	if !strings.Contains(errw2.String(), "declared `unrendered`") {
		t.Errorf("unrendered target not reported:\n%s", errw2.String())
	}
}

// ── the host boundary ───────────────────────────────────────────────────────────────────

// applyHost drives RenderHostPack as `yolo host apply` does: collect across the whole set
// (autonomy off, the host posture), then render each pack against it.
func applyHostPacks(t *testing.T, home string, ownership render.HostOwnership, observe bool,
	packs ...*packload.Pack) []HostRenderResult {
	t.Helper()
	set := packoverlay.Collect(packs, false, nil)
	var all []HostRenderResult
	for _, p := range packs {
		results, err := RenderHostPack(p, home, ownership, observe, set)
		if err != nil {
			t.Fatalf("RenderHostPack(%s): %v", p.Name, err)
		}
		all = append(all, results...)
	}
	return all
}

func listResultFor(results []HostRenderResult, surface string) (HostRenderResult, bool) {
	for _, r := range results {
		if r.Surface == surface {
			return r, true
		}
	}
	return HostRenderResult{}, false
}

// The same five behaviours at the host, under both contracts: `assert` renders the surface
// through rmw (the insert record), `own` through stateful (the list-capture sidecar).
func TestHostConfigListLifecycle(t *testing.T) {
	for _, ownership := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			home := t.TempDir()
			owner := listOwnerPack(t, "", nil)
			results := applyHostPacks(t, home, ownership, false, owner, personalPack(t))
			wantPackages(t, home, "npm:owner-a", "npm:owner-b", listKilo)
			if r, ok := listResultFor(results, "pi/settings"); !ok || !reflect.DeepEqual(r.Lists, []string{"personal"}) {
				t.Fatalf("the host result does not name the contributing pack: %+v", r)
			}

			// The agent appends: kept as the user's.
			editSettings(t, home, appendInstalled)
			applyHostPacks(t, home, ownership, false, owner, personalPack(t))
			wantPackages(t, home, "npm:owner-a", "npm:owner-b", listKilo, listInstalled)

			// The user deletes the pack-added entry: it stays deleted.
			editSettings(t, home, removeEntry(listKilo))
			applyHostPacks(t, home, ownership, false, owner, personalPack(t))
			wantPackages(t, home, "npm:owner-a", "npm:owner-b", listInstalled)

			// The pack is dropped: its entries leave, the agent's own entry stays.
			home2 := t.TempDir()
			applyHostPacks(t, home2, ownership, false, owner, personalPack(t))
			editSettings(t, home2, appendInstalled)
			applyHostPacks(t, home2, ownership, false, owner) // the personal pack is dropped
			wantPackages(t, home2, "npm:owner-a", "npm:owner-b", listInstalled)
		})
	}
}

// "Host apply never removes a user's independently declared matching package when a pack is
// dropped": an entry already in the real file before the first apply is the user's, never
// recorded as inserted, and it survives the drop.
func TestHostConfigListNeverRemovesTheUsersOwnMatchingEntry(t *testing.T) {
	for _, ownership := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, listSettings)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"packages":["npm:mine","`+listKilo+`"]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			owner := listOwnerPack(t, "", nil)
			applyHostPacks(t, home, ownership, false, owner, personalPack(t))
			applyHostPacks(t, home, ownership, false, owner)
			if got := packagesAt(t, home); !containsString(got, listKilo) || !containsString(got, "npm:mine") {
				t.Fatalf("the drop removed the user's own entry: %#v", got)
			}
		})
	}
}

func containsString(list []any, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// OQ-AL2 and the managed floor, at the host.
func TestHostConfigListPrecedence(t *testing.T) {
	home := t.TempDir()
	replacer := &packload.Pack{Name: "replacer", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfigOverlay, Surface: "pi/settings",
			Raw: json.RawMessage(`{"managed":{"packages":["npm:only"]}}`)}},
	}}
	applyHostPacks(t, home, render.OwnershipAssert, false, listOwnerPack(t, "", nil), replacer, personalPack(t))
	wantPackages(t, home, "npm:only", listKilo, "npm:owner-a")

	home2 := t.TempDir()
	applyHostPacks(t, home2, render.OwnershipAssert, false,
		listOwnerPack(t, "", map[string]any{"managed": map[string]any{"packages": []any{"pinned"}}}), personalPack(t))
	wantPackages(t, home2, "pinned")
}

// The refusal at the host is a `refused: config-list …` row naming the surface and its mode,
// in the observe posture as well as the write — and nothing is written.
func TestHostConfigListRefusalRow(t *testing.T) {
	home := t.TempDir()
	owner := listOwnerPack(t, "", map[string]any{"name": "hosts", "codec": "lines",
		"path": "~/.pi/hosts", "defaults": nil})
	for _, observe := range []bool{true, false} {
		results := applyHostPacks(t, home, render.OwnershipAssert, observe, owner,
			listContributorPack(t, "personal", "pi/hosts", "/x", "b"))
		r, ok := listResultFor(results, "pi/hosts")
		if !ok || !strings.HasPrefix(r.Action, "refused: config-list on pi/hosts") ||
			!strings.Contains(r.Action, "mode stateful") {
			t.Fatalf("observe=%v: result = %+v, want a refused: config-list row", observe, r)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "hosts")); !os.IsNotExist(err) {
		t.Fatalf("a refused surface was written (err=%v)", err)
	}
}

// Rule 3 at the host: an agent-owned file holding a non-array at the path is refused as a
// row (the file untouched), not rewritten and not aborting the apply.
func TestHostConfigListTypeConflictIsARefusedRow(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, listSettings)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const content = `{"packages":"npm:a"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, observe := range []bool{true, false} {
		results := applyHostPacks(t, home, render.OwnershipAssert, observe, listOwnerPack(t, "", nil), personalPack(t))
		if r, _ := listResultFor(results, "pi/settings"); !strings.HasPrefix(r.Action, "refused: ") ||
			!strings.Contains(r.Action, "/packages") {
			t.Fatalf("observe=%v: result = %+v, want a refusal naming the path", observe, r)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != content {
		t.Fatalf("the conflicting file was modified:\n%s", data)
	}
}

// REVERT removes exactly the entries yolo inserted: the user's own entries and the owner's
// defaults stay, and the insert record goes with the provenance record.
func TestHostConfigListRevertRemovesOnlyInsertedEntries(t *testing.T) {
	home := t.TempDir()
	owner := listOwnerPack(t, "", nil)
	applyHostPacks(t, home, render.OwnershipAssert, false, owner, personalPack(t))
	editSettings(t, home, appendInstalled)
	applyHostPacks(t, home, render.OwnershipAssert, false, owner, personalPack(t))

	rev, err := RevertHostRender([]*packload.Pack{owner}, home, false)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	var lines []string
	for _, k := range rev.Keys {
		lines = append(lines, k.Key+" "+k.Layer)
	}
	if !containsLine(lines, "/packages \""+listKilo+"\" config-list") {
		t.Fatalf("revert did not report the inserted entry: %v", lines)
	}
	got := packagesAt(t, home)
	if containsString(got, listKilo) || !containsString(got, listInstalled) {
		t.Fatalf("after revert packages = %#v, want the inserted entry gone and the user's kept", got)
	}
	if _, err := os.Stat(render.Host(home, nil, render.OwnershipAssert).ListRecordPath("pi", "settings")); !os.IsNotExist(err) {
		t.Fatalf("the insert record survived the revert (err=%v)", err)
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// ── review regressions ─────────────────────────────────────────────────────────────────

// DELETE, THEN RECREATE, at the jail boundary: the user removes a contributed entry, deletes
// the key, then writes an array of their own. The next boots render exactly what they wrote,
// and the captured deletion no longer holds the key.
func TestJailConfigListDeleteThenRecreateKeepsWhatTheUserWrote(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	owner := listOwnerPack(t, "", nil)
	bootJail(t, e, owner, personalPack(t))
	editSettings(t, e.Home, removeEntry(listKilo))
	bootJail(t, e, owner, personalPack(t))
	editSettings(t, e.Home, func(m map[string]any) { delete(m, "packages") })
	bootJail(t, e, owner, personalPack(t))
	editSettings(t, e.Home, func(m map[string]any) { m["packages"] = []any{"npm:x"} })
	bootJail(t, e, owner, personalPack(t))
	wantPackages(t, e.Home, "npm:x")
	if overlay := readSidecar(t, prismOverlayPath(e, "pi", "settings")); strings.Contains(overlay, "packages") {
		t.Fatalf("the captured deletion outlived the recreated array:\n%s", overlay)
	}
	bootJail(t, e, owner, personalPack(t))
	wantPackages(t, e.Home, "npm:x")
}

// A MANAGED WINDOW is not a user removal. While managed holds the list path the file carries
// managed's array, not yolo's inserted entries; once managed lets go, the contributed entries
// return — at `assert` (rmw) exactly as at `own` (stateful), which recomposes.
func TestHostConfigListManagedWindowDeclinesNothing(t *testing.T) {
	for _, ownership := range []render.HostOwnership{render.OwnershipAssert, render.OwnershipOwn} {
		t.Run(ownership.String(), func(t *testing.T) {
			home := t.TempDir()
			applyHostPacks(t, home, ownership, false, listOwnerPack(t, "", nil), personalPack(t))
			applyHostPacks(t, home, ownership, false,
				listOwnerPack(t, "", map[string]any{"managed": map[string]any{"packages": []any{"pinned"}}}), personalPack(t))
			wantPackages(t, home, "pinned")
			applyHostPacks(t, home, ownership, false, listOwnerPack(t, "", nil), personalPack(t))
			if got := packagesAt(t, home); !containsString(got, listKilo) {
				t.Fatalf("after managed let go packages = %#v — the contributed entry was declined though the user never removed it", got)
			}
			if ownership == render.OwnershipAssert {
				rec := readSidecar(t, render.Host(home, nil, ownership).ListRecordPath("pi", "settings"))
				var recs map[string]map[string]any
				if err := json.Unmarshal([]byte(rec), &recs); err != nil {
					t.Fatal(err)
				}
				if d, _ := recs["/packages"]["declined"].([]any); containsString(d, listKilo) {
					t.Fatalf("the record declined an entry only managed displaced: %s", rec)
				}
			}
		})
	}
}

// A DELETED FILE OR KEY is no evidence of a removal: an rmw render over a file that lost the
// key (or the whole file) re-inserts the contributed entries and declines none of them — or
// deleting ~/.claude.json would refuse every pack's entries for ever.
func TestConfigListRMWDeletedFileOrKeyDeclinesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		erase func(t *testing.T, home string)
	}{
		{"file", func(t *testing.T, home string) {
			if err := os.Remove(filepath.Join(home, listSettings)); err != nil {
				t.Fatal(err)
			}
		}},
		{"key", func(t *testing.T, home string) {
			editSettings(t, home, func(m map[string]any) { delete(m, "packages") })
		}},
	} {
		t.Run("host/"+tc.name, func(t *testing.T) {
			home := t.TempDir()
			owner := listOwnerPack(t, "", nil)
			applyHostPacks(t, home, render.OwnershipAssert, false, owner, personalPack(t))
			tc.erase(t, home)
			applyHostPacks(t, home, render.OwnershipAssert, false, owner, personalPack(t))
			if got := packagesAt(t, home); !containsString(got, listKilo) {
				t.Fatalf("packages = %#v — the contributed entry did not come back", got)
			}
			if rec := readSidecar(t, render.Host(home, nil, render.OwnershipAssert).ListRecordPath("pi", "settings")); strings.Contains(rec, `"declined": [
      "`+listKilo) {
				t.Fatalf("a deleted %s declined the entry: %s", tc.name, rec)
			}
		})
		t.Run("jail/"+tc.name, func(t *testing.T) {
			e, _ := overlayRenderEnv(t)
			owner := listOwnerPack(t, "rmw", nil)
			bootJail(t, e, owner, personalPack(t))
			tc.erase(t, e.Home)
			bootJail(t, e, owner, personalPack(t))
			if got := packagesAt(t, e.Home); !containsString(got, listKilo) {
				t.Fatalf("packages = %#v — the contributed entry did not come back", got)
			}
		})
	}
}

// RULE 3, the intermediate-parent half, at an rmw surface: a non-object ANCESTOR of the list
// path in the agent-owned file is refused as a row (observe and write), naming the ancestor,
// and the file is left byte-identical — never overwritten to make room for the array.
func TestConfigListRMWBlockedParentIsRefusedAndTheFileUntouched(t *testing.T) {
	const content = `{"models":"keep-me"}`
	models := listContributorPack(t, "personal", "pi/settings", "/models/tags", "t1")
	t.Run("host", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, listSettings)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, observe := range []bool{true, false} {
			results := applyHostPacks(t, home, render.OwnershipAssert, observe, listOwnerPack(t, "", nil), models)
			r, _ := listResultFor(results, "pi/settings")
			if !strings.HasPrefix(r.Action, "refused: ") || !strings.Contains(r.Action, "/models holds a string") {
				t.Fatalf("observe=%v: result = %+v, want a refusal naming the blocking ancestor", observe, r)
			}
		}
		if data, _ := os.ReadFile(path); string(data) != content {
			t.Fatalf("the conflicting file was modified:\n%s", data)
		}
	})
	t.Run("jail", func(t *testing.T) {
		e, errw := overlayRenderEnv(t)
		path := filepath.Join(e.Home, listSettings)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		ConfigurePackSurfaces(e, []*packload.Pack{listOwnerPack(t, "rmw", nil), models})
		if !strings.Contains(errw.String(), "/models holds a string") {
			t.Fatalf("the rmw refusal was not reported:\n%s", errw.String())
		}
		if data, _ := os.ReadFile(path); string(data) != content {
			t.Fatalf("the conflicting file was modified:\n%s", data)
		}
	})
}

// RULE 3 at the host under `own` (stateful): a lower layer holding a non-array at the path is
// a `refused:` row in the observe dry run AND the write — never "unchanged", and never an
// error that aborts the whole apply.
func TestHostConfigListTypeConflictUnderOwnIsARefusedRow(t *testing.T) {
	home := t.TempDir()
	owner := listOwnerPack(t, "", map[string]any{"defaults": map[string]any{"packages": "npm:a"}})
	for _, observe := range []bool{true, false} {
		results := applyHostPacks(t, home, render.OwnershipOwn, observe, owner, personalPack(t))
		if r, _ := listResultFor(results, "pi/settings"); !strings.HasPrefix(r.Action, "refused: ") ||
			!strings.Contains(r.Action, "/packages") {
			t.Fatalf("observe=%v: result = %+v, want a refusal naming the path", observe, r)
		}
	}
	if _, err := os.Stat(filepath.Join(home, listSettings)); !os.IsNotExist(err) {
		t.Fatalf("a refused surface was written (err=%v)", err)
	}
}

// REVERT UNDER `own` keeps the user's own entries: an in-jail append, and an entry the user's
// file already held before the first apply, are theirs — revert withdraws what yolo inserted
// and nothing the user wrote.
func TestHostConfigListRevertUnderOwnKeepsTheUsersEntries(t *testing.T) {
	t.Run("appended", func(t *testing.T) {
		home := t.TempDir()
		owner := listOwnerPack(t, "", nil)
		applyHostPacks(t, home, render.OwnershipOwn, false, owner, personalPack(t))
		editSettings(t, home, appendInstalled)
		applyHostPacks(t, home, render.OwnershipOwn, false, owner, personalPack(t))
		if _, err := RevertHostRender([]*packload.Pack{owner}, home, false); err != nil {
			t.Fatal(err)
		}
		if got := packagesAt(t, home); !containsString(got, listInstalled) || containsString(got, listKilo) {
			t.Fatalf("after revert packages = %#v, want the user's entry kept and the inserted one gone", got)
		}
	})
	t.Run("adopted", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, listSettings)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"packages":["npm:mine"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		owner := listOwnerPack(t, "", nil)
		applyHostPacks(t, home, render.OwnershipOwn, false, owner, personalPack(t))
		if _, err := RevertHostRender([]*packload.Pack{owner}, home, false); err != nil {
			t.Fatal(err)
		}
		if got := packagesAt(t, home); !containsString(got, "npm:mine") {
			t.Fatalf("after revert packages = %#v — the user's pre-existing entry was deleted", got)
		}
	})
}

// A HOST OWNERSHIP SWITCH keeps a pack drop working: the entries yolo inserted under one
// contract are still yolo's under the other, so dropping the pack removes them, while the
// user's own entry stays.
func TestHostConfigListOwnershipSwitchKeepsTheDropWorking(t *testing.T) {
	for _, order := range [][2]render.HostOwnership{
		{render.OwnershipAssert, render.OwnershipOwn},
		{render.OwnershipOwn, render.OwnershipAssert},
	} {
		t.Run(order[0].String()+"-then-"+order[1].String(), func(t *testing.T) {
			home := t.TempDir()
			owner := listOwnerPack(t, "", nil)
			applyHostPacks(t, home, order[0], false, owner, personalPack(t))
			editSettings(t, home, appendInstalled)
			applyHostPacks(t, home, order[0], false, owner, personalPack(t))
			applyHostPacks(t, home, order[1], false, owner, personalPack(t))
			wantPackages(t, home, "npm:owner-a", "npm:owner-b", listKilo, listInstalled)
			applyHostPacks(t, home, order[1], false, owner) // the personal pack is dropped
			wantPackages(t, home, "npm:owner-a", "npm:owner-b", listInstalled)
		})
	}
}

// REVERT of a key ONLY the list created: the owner declares no `packages`, so once yolo's
// inserted entries are withdrawn the key is yolo's creation and is deleted — rendered twice
// first, so the second render's `config-list` label has to survive the `host` guess
// (keepListCreated).
func TestHostConfigListRevertDeletesAKeyOnlyTheListCreated(t *testing.T) {
	home := t.TempDir()
	owner := listOwnerPack(t, "", map[string]any{"defaults": map[string]any{"theme": "dark"}})
	contrib := listContributorPack(t, "personal", "pi/settings", "/packages", listKilo)
	applyHostPacks(t, home, render.OwnershipAssert, false, owner, contrib)
	applyHostPacks(t, home, render.OwnershipAssert, false, owner, contrib)
	if _, err := RevertHostRender([]*packload.Pack{owner}, home, false); err != nil {
		t.Fatal(err)
	}
	if _, present := readRenderedJSON(t, home, listSettings)["packages"]; present {
		t.Fatalf("revert left the key only the list created: %s", readSidecar(t, filepath.Join(home, listSettings)))
	}
}

// An `unrendered` target at the host is inert and named as a skipped row.
func TestHostConfigListUnrenderedTargetIsASkippedRow(t *testing.T) {
	home := t.TempDir()
	results := applyHostPacks(t, home, render.OwnershipAssert, false, listOwnerPack(t, "unrendered", nil), personalPack(t))
	var rows []string
	for _, r := range results {
		rows = append(rows, r.Surface+": "+r.Action)
	}
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "unrendered") || !strings.Contains(joined, "config-list") {
		t.Fatalf("no skipped row for the unrendered config-list target:\n%s", joined)
	}
	if _, err := os.Stat(filepath.Join(home, listSettings)); !os.IsNotExist(err) {
		t.Fatalf("an unrendered target was written (err=%v)", err)
	}
}

// ...and a contributed entry the user REMOVED under one contract stays removed under the
// other: `assert` records it declined, `own` records it as a per-entry removal, and each
// reads the other's record.
func TestHostConfigListOwnershipSwitchKeepsTheUsersRemoval(t *testing.T) {
	for _, order := range [][2]render.HostOwnership{
		{render.OwnershipAssert, render.OwnershipOwn},
		{render.OwnershipOwn, render.OwnershipAssert},
	} {
		t.Run(order[0].String()+"-then-"+order[1].String(), func(t *testing.T) {
			home := t.TempDir()
			owner := listOwnerPack(t, "", nil)
			applyHostPacks(t, home, order[0], false, owner, personalPack(t))
			editSettings(t, home, removeEntry(listKilo))
			applyHostPacks(t, home, order[0], false, owner, personalPack(t))
			applyHostPacks(t, home, order[1], false, owner, personalPack(t))
			applyHostPacks(t, home, order[1], false, owner, personalPack(t))
			wantPackages(t, home, "npm:owner-a", "npm:owner-b")
		})
	}
}
