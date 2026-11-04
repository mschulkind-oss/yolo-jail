package run

// legacycompat_test.go holds the owner's compatibility ruling to the launch's own gates
// (2026-11-03): every config that worked in the previous release keeps working, UNCHANGED, with
// the same meaning, and no edit, command, `yolo allow` or migration is required of the user.
//
// THE CORPUS (testdata/legacycompat/) is synthetic: no owner file is copied, and every name and
// path in it is invented.
//
//   - v0151-generated.json: one case per top-level key v0.15.1's schema accepted, at each scope,
//     and per loophole switch and setting in v0.15.1's shipped manifests, at the setting's own
//     scope. Each case is one v0.15.1 ACCEPTED, with what v0.15.1 made of it (`v0151`): the
//     loopholes its launch started and the settings each was handed. probe/ is the program that
//     produced it, run in a v0.15.1 checkout (probe/README).
//   - realistic/<case>/: whole homes and workspaces shaped like the owner's (symlinked dotfiles
//     configs, include layers, local packs, a committed service workspace, legacy git grants),
//     plus the TOML and JSONC mixes the owner hit. v0151-realistic.json is v0.15.1's meaning for
//     the ones v0.15.1 could read; a `current_only` case (a TOML file, a spelling newer than
//     v0.15.1) carries its expected meaning in case.json instead.
//
// THE MATRIX. Each case runs in every cell it has: the user config as written (legacy JSONC) and
// converted by `yolo config migrate`'s own planner to TOML, times the workspace config as
// written, converted, or absent when the case has none.
//
// THE GATES, each the current tree's in-process equivalent of a launch step:
//
//   - migrate-user / migrate-workspace: the conversion that makes a TOML cell (Plan, then Apply);
//   - migrate-offer: the launch's migrate offer's plan over what is still legacy in the cell;
//   - load: the launch's config gate, loadAndValidateConfig (load, validate, the pack switch,
//     the capability gate);
//   - packs: config.LoadPacks, the selection every pack reader resolves;
//   - inherit: the jail crossing, inheritedScopeWithSelector (config.RenderInheritedDelivery
//     with configmigrate.LowerInheritedUserScope), for podman: ANY USER SCOPE THE HOST ACCEPTS
//     MUST CROSS INTO THE JAIL;
//   - jail-load: the jail's own read of what crossed, with YOLO_VERSION set;
//   - meaning: the launch's resolution compared with v0.15.1's (meaningMismatches).
//
// `yolo check` (internal/cli/check) runs config.ValidateConfig over its own merge; it is not
// callable from this package, so the load gate stands in for it.
//
// THE ALLOWLIST. Every refusal or mismatch found is a knownLegacyBreaks entry naming its pending
// fix. A finding no entry covers fails the test, and so does an entry that no longer occurs, so
// a fix removes its entry in the same commit.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// legacyBreak is one known refusal or meaning mismatch of a legacy config: Case is a pattern
// over case ids (legacyGlob), Gate one over the gate it occurs at, Contains a substring of its text,
// and Reason "pending fix: <id>" naming the fix that removes it. The ids are this file's own
// labels (lc-…), coined here so each pending fix has a name until it has an issue.
type legacyBreak struct {
	Case, Gate, Contains, Reason string
}

// ruledLegacyRefusals is every refusal of a config v0.15.1 accepted that the owner ruled is the
// behavior, outside docs/design/legacy-config-compat.md's rule (which is scoped to the TOML
// switch): retirements with their own transition (OQ-LC1) and a security refusal (OQ-LC3). Each
// must still occur, so a refusal that goes away silently is caught too.
var ruledLegacyRefusals = []legacyBreak{
	{"toplevel/env_sources/workspace", "*", "is in the workspace config, outside the workspace",
		"ruled: OQ-LC3 (the 2026-10-29 security refusal stands)"},
	{"toplevel/mcp_presets/*", "load", "config.mcp_presets: REMOVED", "ruled: OQ-LC1 (OQ-MP6's retirement stands)"},
	{"toplevel/mcp_presets/*", "migrate-*", "mcp_presets - [", "ruled: OQ-LC1 (OQ-MP6's retirement stands)"},
	{"loophole/openai-auth-broker/*", "load", "openai-auth-broker", "ruled: OQ-LC1 (OQ-PE3's rename stands)"},
	// The same rename, met at the user config's pack source once the workspace converts.
	{"loophole/openai-auth-broker/enabled/workspace", "load", `"openai-auth" is now "codex-auth"`, "ruled: OQ-LC1 (OQ-PE3's rename stands)"},
	{"loophole/openai-auth-broker/*", "migrate-*", "only shipped packs may be named without a source",
		"ruled: OQ-LC1 (OQ-PE3's rename stands)"},
	// Named setups replace `runtime` and `confinement` (named-setups.md §10; the owner's MT-R3: "A
	// removed spelling is refused naming its replacement; no aliases"). A user file converts:
	// `yolo config migrate` writes the setup (SUP-D14), so its TOML cells launch. A project's
	// never converts, as a project cannot choose where its agent runs, and its refusal names the
	// unset that removes it.
	{"toplevel/runtime/*", "load", "config.runtime: REMOVED", "ruled: MT-R3 (named setups replace the key)"},
	{"toplevel/confinement/*", "load", "config.confinement: REMOVED", "ruled: MT-R3 (named setups replace the key)"},
	{"toplevel/runtime/workspace", "migrate-*", "yolo config unset runtime --project",
		"ruled: MT-R3 (a project cannot choose a setup, named-setups.md §8)"},
	{"toplevel/confinement/workspace", "migrate-*", "yolo config unset confinement --project",
		"ruled: MT-R3 (a project cannot choose a setup, named-setups.md §8)"},
}

// knownLegacyBreaks is every way a config that worked in v0.15.1 fails today. It only shrinks.
var knownLegacyBreaks = []legacyBreak{
	// The launch's config gate refuses what v0.15.1 accepted.

	// The migration (the launch's offer, and the conversion a TOML cell needs) cannot convert a
	// legacy file exactly.
	{"loophole/*/enabled/user", "migrate-*", "user config: loopholes - ",
		"pending fix: lc-migrate-user-loophole-switch"},
	{"toplevel/loopholes/user", "migrate-*", "user config: loopholes - ",
		"pending fix: lc-migrate-user-loophole-switch"},
	{"loophole/serial/enabled/user", "migrate-offer", "the loophole grants each workspace holds",
		"pending fix: lc-migrate-user-loophole-switch"},
	{"loophole/*/workspace", "migrate-*", "project config: loopholes - ",
		"pending fix: lc-migrate-ws-loophole-switch"},
	{"realistic/*", "migrate-*", `project config: loopholes - {"host-processes"`,
		"pending fix: lc-migrate-ws-loophole-switch"},
	{"realistic/toml-user-legacy-ws-journal", "migrate-workspace", `project config: loopholes - {"journal"`,
		"pending fix: lc-migrate-ws-loophole-switch"},
	// A workspace `enabled: false` on an off-switch loophole launches off, but the project's
	// `off` it would convert to is not built (docs/design/legacy-config-compat.md LC-D3).
	{"realistic/workspace-off-switches", "migrate-*", `project config: loopholes - {"aws-auth": {"enabled": false}`,
		"pending fix: lc-migrate-ws-loophole-switch (the project's off is not built, LC-D3)"},

	// The config launches, but means less than it did: a user switch of a valued grant reads as
	// on_request under a TOML user config, and TOML has no spelling for the user-sourced request.
	{"loophole/serial/enabled/user", "meaning", `serial: grant "devices" is "requestable", v0.15.1 allowed it`,
		"pending fix: lc-migrate-user-loophole-switch (the converted user ceiling lists on_request, which nothing requests)"},
	{"realistic/personal-desktop", "meaning", `serial: grant "devices" is "requestable", v0.15.1 allowed it`,
		"pending fix: lc-migrate-user-loophole-switch (the converted user ceiling lists on_request, which nothing requests)"},
}

const legacyCompatDir = "testdata/legacycompat"

// legacyMeaning is what a launch made of a config: the loopholes it started and the settings each
// was handed. It is the shape probe/ writes for v0.15.1.
type legacyMeaning struct {
	Enabled  []string                  `json:"enabled"`
	Settings map[string]map[string]any `json:"settings,omitempty"`
}

// legacyCase is one corpus case, materialized under base as home/ and ws/.
type legacyCase struct {
	ID    string
	About string
	// write materializes the case's files under base (home/ and ws/).
	write func(t *testing.T, base string)
	// WorkspaceFile is the per-workspace file's loophole switches, as `yolo loopholes enable`
	// wrote them before the switch was retired.
	WorkspaceFile map[string]bool
	// V0151 is v0.15.1's meaning; nil for a current_only case.
	V0151 *legacyMeaning
	// ExpectEnabled is a current_only case's loopholes that must run.
	ExpectEnabled []string
	// ExpectOff is a current_only case's loopholes its config keeps off (LC-D3's off switch):
	// one that runs is a mismatch, in every cell, converted ones included.
	ExpectOff []string
	// ExpectGitGrants is the git broker grant kinds the workspace must hold.
	ExpectGitGrants []string
	// ExpectRefused, when set, is text the launch's refusal must hold: a case the owner ruled
	// is an error (a dangling config name beside its sibling, RC-D59 and RC-D67), so its
	// launch passing is the finding.
	ExpectRefused string
}

type legacyFinding struct {
	Case, Cell, Gate, Text string
}

func (f legacyFinding) String() string {
	return fmt.Sprintf("%s [%s] %s: %s", f.Case, f.Cell, f.Gate, f.Text)
}

func TestLegacyConfigsLaunchUnchanged(t *testing.T) {
	cases := append(generatedLegacyCases(t), realisticLegacyCases(t)...)
	if testing.Short() {
		// The whole matrix is the landing gate's; -short runs every case once, in its
		// most-converted cell, which is where the migration and the crossing both act.
		t.Logf("-short: %d cases, one cell each", len(cases))
	}
	var findings []legacyFinding
	cells := 0
	only := os.Getenv("YOLO_LEGACYCOMPAT_CASE") // a legacyGlob pattern, for working on one case
	for _, c := range cases {
		if only != "" && !legacyGlob(only, c.ID) {
			continue
		}
		for _, cell := range legacyCells(c) {
			if testing.Short() && !cell.last {
				continue
			}
			cells++
			findings = append(findings, runLegacyCell(t, c, cell)...)
		}
	}
	t.Logf("corpus: %d cases, %d cells, %d findings", len(cases), cells, len(findings))
	if dump := os.Getenv("YOLO_LEGACYCOMPAT_DUMP"); dump != "" {
		var b strings.Builder
		for _, f := range findings {
			b.WriteString(f.String() + "\n")
		}
		if err := os.WriteFile(dump, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	used := make([]bool, len(knownLegacyBreaks))
	ruledUsed := make([]bool, len(ruledLegacyRefusals))
	for _, f := range findings {
		covered := false
		for i, k := range ruledLegacyRefusals {
			if legacyGlob(k.Case, f.Case) && legacyGlob(k.Gate, f.Gate) && strings.Contains(f.Text, k.Contains) {
				ruledUsed[i], covered = true, true
			}
		}
		for i, k := range knownLegacyBreaks {
			if legacyGlob(k.Case, f.Case) && legacyGlob(k.Gate, f.Gate) && strings.Contains(f.Text, k.Contains) {
				used[i], covered = true, true
			}
		}
		if !covered {
			t.Errorf("a config v0.15.1 accepted no longer launches unchanged with the same meaning:\n  %s\n"+
				"Fix it; only a break with a pending fix is listed in knownLegacyBreaks.", f)
		}
	}
	for i, k := range ruledLegacyRefusals {
		if !ruledUsed[i] && !testing.Short() && only == "" {
			t.Errorf("ruledLegacyRefusals entry {%q, %q, %q} (%s) no longer occurs: the ruled refusal went away",
				k.Case, k.Gate, k.Contains, k.Reason)
		}
	}
	for i, k := range knownLegacyBreaks {
		if (testing.Short() || only != "") && !used[i] {
			continue // the entry may occur in a cell -short skips
		}
		if !used[i] {
			t.Errorf("knownLegacyBreaks entry {%q, %q, %q} (%s) no longer occurs: remove the entry",
				k.Case, k.Gate, k.Contains, k.Reason)
		}
	}
}

// legacyCell is one combination of the user and workspace formats a case runs in.
// userTOML and wsTOML convert the case's legacy file first; the written fields say the case
// already wrote that file in TOML.
type legacyCell struct {
	userTOML, wsTOML, wsAbsent bool
	userWrittenTOML            bool
	wsWrittenTOML              bool
	last                       bool // the most-converted cell, the one -short runs
}

// String names the cell by what the launch reads: "jsonc" a legacy file, "toml" a TOML one the
// case wrote, "toml*" one this test converted from the case's legacy file.
func (c legacyCell) String() string {
	u, w := "user=jsonc", "ws=jsonc"
	switch {
	case c.userTOML:
		u = "user=toml*"
	case c.userWrittenTOML:
		u = "user=toml"
	}
	switch {
	case c.wsAbsent:
		w = "ws=absent"
	case c.wsTOML:
		w = "ws=toml*"
	case c.wsWrittenTOML:
		w = "ws=toml"
	}
	return u + "," + w
}

// legacyCells is every cell of c: a legacy user file is also run converted; a legacy workspace
// file is also run converted; a case with no workspace file runs with none.
func legacyCells(c legacyCase) []legacyCell {
	base := t0Materialize(c)
	users := []bool{false}
	if base.userLegacy {
		users = []bool{false, true}
	}
	var out []legacyCell
	for _, u := range users {
		cell := legacyCell{userTOML: u, userWrittenTOML: base.userTOML, wsWrittenTOML: base.wsTOML}
		if !base.wsPresent {
			cell.wsAbsent = true
			out = append(out, cell)
			continue
		}
		out = append(out, cell)
		if base.wsLegacy {
			cell.wsTOML = true
			out = append(out, cell)
		}
	}
	out[len(out)-1].last = true
	return out
}

type caseShape struct{ userLegacy, userTOML, wsPresent, wsLegacy, wsTOML bool }

// t0Materialize writes c once into a throwaway directory to read its shape.
func t0Materialize(c legacyCase) caseShape {
	dir, err := os.MkdirTemp("", "legacycompat-shape-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	c.write(nil, dir)
	exists := func(rel string) bool {
		_, err := os.Lstat(filepath.Join(dir, rel))
		return err == nil
	}
	userLegacyLink := false
	if fi, err := os.Lstat(filepath.Join(dir, "home/.config/yolo-jail/config.jsonc")); err == nil {
		// A dangling link is no config (the dangling-link case), so it is no legacy cell either.
		_, statErr := os.Stat(filepath.Join(dir, "home/.config/yolo-jail/config.jsonc"))
		userLegacyLink = fi.Mode()&fs.ModeSymlink == 0 || statErr == nil
	}
	return caseShape{
		userLegacy: userLegacyLink && !exists("home/.config/yolo-jail/config.toml"),
		userTOML:   exists("home/.config/yolo-jail/config.toml"),
		wsPresent:  exists("ws/yolo-jail.jsonc") || exists("ws/yolo-jail.toml"),
		wsLegacy:   exists("ws/yolo-jail.jsonc"),
		wsTOML:     exists("ws/yolo-jail.toml"),
	}
}

func runLegacyCell(t *testing.T, c legacyCase, cell legacyCell) []legacyFinding {
	t.Helper()
	var out []legacyFinding
	note := func(gate, text string) {
		out = append(out, legacyFinding{Case: c.ID, Cell: cell.String(), Gate: gate, Text: oneLine(text)})
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.write(t, base)
	home, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
	for _, d := range []string{filepath.Join(home, ".config", "yolo-jail"), ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if cell.wsAbsent {
		_ = os.Remove(filepath.Join(ws, "yolo-jail.jsonc"))
	}
	restore := legacyEnv(t, home)
	defer restore()
	for name, on := range c.WorkspaceFile {
		writeLegacyWorkspaceFile(t, ws, name, on)
	}
	userDir := filepath.Join(home, ".config", "yolo-jail")

	convert := func(gate string, req configmigrate.Request) bool {
		g, err := configmigrate.Discover(req)
		if err != nil {
			note(gate, "discover: "+err.Error())
			return false
		}
		plan, err := configmigrate.Plan(g)
		if text := planProblem(plan, err); text != "" {
			note(gate, text)
			return false
		}
		if _, err := configmigrate.Apply(plan, configmigrate.ApplyOptions{}); err != nil {
			note(gate, "apply: "+err.Error())
			return false
		}
		return true
	}
	if cell.userTOML && !convert("migrate-user", configmigrate.Request{UserConfigDir: userDir}) {
		return out
	}
	if cell.wsTOML && !convert("migrate-workspace", configmigrate.Request{Workspace: ws}) {
		return out
	}
	// The launch's migrate offer over what is left legacy: a dry run, as a launch without a
	// terminal makes it.
	if g, err := configmigrate.Discover(configmigrate.Request{Workspace: ws, UserConfigDir: userDir}); err == nil {
		plan, err := configmigrate.Plan(g)
		if text := planProblem(plan, err); text != "" {
			note("migrate-offer", text)
		}
	}

	var buf strings.Builder
	o := &Options{Workspace: ws, Stdout: &buf, Stderr: &buf,
		IsTTYStdin: func() bool { return false }, IsTTYStdout: func() bool { return false },
		IsTTYStderr: func() bool { return false }, Getenv: os.Getenv,
		Now: func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) }}
	cfg, ok := o.loadAndValidateConfig()
	if c.ExpectRefused != "" {
		if ok {
			note("load", "launched; the owner ruled it an error, so it must be refused naming "+c.ExpectRefused)
		} else if !strings.Contains(buf.String(), c.ExpectRefused) {
			note("load", "refused, but not naming "+c.ExpectRefused+": "+buf.String())
		}
		return out
	}
	if !ok {
		note("load", buf.String())
		return out
	}
	if _, err := config.LoadPacks(func(string) {}); err != nil {
		note("packs", err.Error())
	}
	buf.Reset()
	files, err := o.inheritedScopeWithSelector("podman", false,
		func(config.InheritScope, configdoc.Format) (bool, error) { return false, nil })
	if err != nil {
		note("inherit", err.Error())
	} else if text := jailLoad(t, base, ws, files); text != "" {
		note("jail-load", text)
	}
	for _, m := range meaningMismatches(o, cfg, c) {
		note("meaning", m)
	}
	return out
}

// planProblem is a migration plan's refusal, as the launch's offer reads it: an error, a
// refusal, a key it cannot convert, or a result it cannot prove equal.
func planProblem(plan *configmigrate.MigrationPlan, err error) string {
	if err != nil {
		return "plan: " + err.Error()
	}
	if plan.Refused != nil {
		return "refused: " + plan.Refused.Error()
	}
	var parts []string
	for _, nc := range plan.NotConverted {
		parts = append(parts, "not converted: "+nc.Path+": "+nc.Reason)
	}
	if !plan.Equal && len(plan.Targets) > 0 {
		parts = append(parts, (&configmigrate.InequalityError{Plan: plan}).Error())
	}
	return strings.Join(parts, "; ")
}

// jailLoad reads what crossed as the jail does: the inherited files in a jail home, the same
// workspace, YOLO_VERSION set. It returns the refusal text, or "".
func jailLoad(t *testing.T, base, ws string, files []inheritScopeFile) string {
	t.Helper()
	jailHome := filepath.Join(base, "jail-home")
	for _, f := range files {
		p := filepath.Join(jailHome, f.rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	restore := legacyEnv(t, jailHome)
	defer restore()
	t.Setenv("YOLO_VERSION", "0.0.0-legacycompat")
	defer t.Setenv("YOLO_VERSION", "")
	cfg, err := config.LoadConfig(ws, true, func(string) {})
	if err != nil {
		return err.Error()
	}
	errs, _ := config.ValidateConfig(cfg, ws, loopholeResolver())
	return strings.Join(errs, "; ")
}

// legacyEnv points HOME at home for one cell, with a fresh pack-module and embedded-pack lease
// under it, and returns what puts the previous HOME's back.
func legacyEnv(t *testing.T, home string) func() {
	t.Helper()
	prev := os.Getenv("HOME")
	set := func(h string) {
		packload.ReleaseEmbedded()
		t.Setenv("HOME", h)
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("YOLO_VERSION", "")
		t.Setenv("YOLO_WORKSPACE", "")
		loopholes.ResetPackModules()
	}
	set(home)
	return func() { set(prev) }
}

// writeLegacyWorkspaceFile writes the per-workspace file as `yolo loopholes enable` wrote it
// before the switch was retired (v0.15.1's writeWorkspaceFile).
func writeLegacyWorkspaceFile(t *testing.T, ws, name string, on bool) {
	t.Helper()
	p := config.WorkspaceFilePath(ws)
	doc := map[string]any{"workspace": ws, "loopholes": map[string]any{name: map[string]any{"enabled": on}}}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	header := "// yolo's per-workspace file for the workspace named below. `yolo loopholes enable` and\n" +
		"// `yolo loopholes disable` write it; it applies to that workspace alone, from its next fresh\n" +
		"// launch, and no jail can read it. You may edit it by hand, but yolo rewrites the whole file\n" +
		"// the next time one of those commands runs, so a comment you add is not kept.\n"
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(header+string(body)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// meaningMismatches compares what this launch makes of the cell's config with what v0.15.1 made
// of it (or, for a current_only case, with the case's expectation):
//
//   - a loophole v0.15.1 started must run, and hold every grant its legacy switch, values and
//     settings map to (loopholedecl.Grant's legacy_* fields), with the same values;
//   - every other setting v0.15.1 handed it must resolve to the same value;
//   - a grant-declaring loophole v0.15.1 did not start must hold no grant;
//   - a loophole v0.15.1 started through the per-workspace file whose broker needs a read grant
//     must hold one.
//
// A grant is the same meaning when it is granted, or pending when the legacy line that gave it was
// in a config file (the workspace's own, or the user's): pending is requested and admitted, so the
// gate's y grants it. For the workspace's file v0.15.1's config-change prompt asked the same
// question; for the user's, one y per project is the owner's rule for a user-wide grant (LG-D12,
// docs/design/legacy-config-compat.md LC-D2), the one host step P4 allows. A switch in the
// per-workspace file was the human's own host command, so it must be granted outright.
func meaningMismatches(o *Options, cfg *jsonx.OrderedMap, c legacyCase) []string {
	var out []string
	set := loopholes.NewHostSet(loopholes.LaunchBlock(cfg))
	running := map[string]*loopholes.Loophole{}
	for _, lp := range set.Enabled() {
		running[lp.Name] = lp
	}
	byName := map[string]*loopholes.Loophole{}
	for _, lp := range set.All() {
		byName[lp.Name] = lp
	}
	standings := o.loopholeGrantStandings("podman", cfg)
	check := legacyScopeCheck(o, cfg)
	// A loophole inert on this platform (macos-log on Linux) ran nothing in v0.15.1 either.
	inert := map[string]bool{}
	if check != nil {
		for _, in := range check.InertLoopholeGrants {
			inert[in.Loophole] = true
		}
	}
	_, statErr := os.Stat(filepath.Join(o.Workspace, ".git", "config"))
	hasRemote := statErr == nil

	want := c.V0151
	if want == nil {
		want = &legacyMeaning{Enabled: c.ExpectEnabled}
	}
	started := map[string]bool{}
	for _, name := range want.Enabled {
		started[name] = true
	}
	for _, name := range c.ExpectOff {
		if running[name] != nil {
			out = append(out, fmt.Sprintf("%s: the config keeps it off (enabled: false, or off = true); it runs", name))
		}
	}
	for _, name := range want.Enabled {
		lp := running[name]
		if lp == nil {
			out = append(out, fmt.Sprintf("%s: v0.15.1 started it; it does not run", name))
			continue
		}
		legacySettings := want.Settings[name]
		covered := map[string]bool{}
		for _, g := range lp.Grants {
			if g.LegacyValues != "" {
				covered[g.LegacyValues] = true
			}
			if g.LegacySetting != "" {
				covered[g.LegacySetting] = true
			}
		}
		for _, g := range lp.Grants {
			if inert[name] {
				break
			}
			expect := g.LegacyEnable
			if g.LegacySetting != "" {
				expect = legacySettings[g.LegacySetting] == true
			}
			st := standings[name][g.Kind]
			if expect && g.Valued && g.LegacyValues != "" {
				if v, ok := legacySettings[g.LegacyValues].([]any); ok && len(v) == 0 {
					// v0.15.1 ran it on an empty list, which showed nothing (host-processes'
					// `visible: []` exits "empty allowlist"): no access then, none now.
					continue
				}
			}
			granted := st.Standing == config.LoopholeGrantGranted ||
				(legacySwitchInConfig(c, name) && st.Standing == config.LoopholeGrantPending)
			switch {
			case expect && !granted:
				out = append(out, fmt.Sprintf("%s: grant %q is %q, v0.15.1 allowed it (%s)", name, g.Kind,
					orNone(st.Standing), legacyGrantWhy(g)))
			case !expect && st.Standing == config.LoopholeGrantGranted:
				out = append(out, fmt.Sprintf("%s: grant %q is granted, v0.15.1 did not allow it", name, g.Kind))
			}
			// A current_only case recorded no v0.15.1 values to compare against.
			if expect && granted && g.Valued && c.V0151 != nil {
				wantVals := g.LegacyDefaultValues
				if v, ok := legacySettings[g.LegacyValues].([]any); ok && g.LegacyValues != "" {
					wantVals = toStrings(v)
				}
				if g.LegacyValues != "" && !sameSet(wantVals, st.Values) {
					out = append(out, fmt.Sprintf("%s: grant %q values %v, v0.15.1 allowed %v (%s)",
						name, g.Kind, st.Values, wantVals, g.LegacyValues))
				}
			}
		}
		if len(legacySettings) > 0 {
			values, _ := loopholes.ResolveSettings(lp, suppliedSettings(loopholes.ConfigBlock(cfg), name))
			keys := make([]string, 0, len(legacySettings))
			for k := range legacySettings {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if covered[k] {
					continue
				}
				got, ok := values.Get(k)
				if !ok {
					out = append(out, fmt.Sprintf("%s: setting %q (v0.15.1: %v) is no longer resolved", name, k, legacySettings[k]))
					continue
				}
				if !reflect.DeepEqual(normalizeJSON(jsonx.Plain(got)), normalizeJSON(legacySettings[k])) {
					out = append(out, fmt.Sprintf("%s: setting %q resolves to %v, v0.15.1 handed %v",
						name, k, jsonx.Plain(got), legacySettings[k]))
				}
			}
		}
		// Without a remote the broker's scope is empty, in v0.15.1 too, so only a workspace with
		// one says whether its calls run.
		if hasRemote && lp.Brokered != nil && lp.Brokered.Source == "github" && !brokerHolds(check, "github", "read") {
			out = append(out, fmt.Sprintf("%s: holds no read grant, so every call is refused; v0.15.1 ran them", name))
		}
	}
	for name, lp := range byName {
		if started[name] || len(lp.Grants) == 0 {
			continue
		}
		for kind, st := range standings[name] {
			if st.Standing == config.LoopholeGrantGranted {
				out = append(out, fmt.Sprintf("%s: grant %q is granted, v0.15.1 did not start it", name, kind))
			}
		}
	}
	for _, kind := range c.ExpectGitGrants {
		if !brokerHolds(check, "git", kind) {
			out = append(out, fmt.Sprintf("git broker: no %q grant, the legacy config granted it", kind))
		}
	}
	sort.Strings(out)
	return out
}

func legacyScopeCheck(o *Options, cfg *jsonx.OrderedMap) *config.ScopeCheck {
	read, err := config.ReadWorkspaceForGate(o.Workspace)
	if err != nil {
		return nil
	}
	return CollectScopeCheck(ScopeInput{Workspace: o.Workspace, Merged: cfg, Runtime: "podman",
		Allow: o.loopholeAllow("podman", cfg), Read: read})
}

func brokerHolds(check *config.ScopeCheck, source, kind string) bool {
	if check == nil {
		return false
	}
	for _, s := range check.Sources {
		if s.Source != source {
			continue
		}
		for _, g := range s.Grants {
			if g.Kind == kind {
				return true
			}
		}
	}
	return false
}

// legacySwitchInConfig is whether the line that switched name on in v0.15.1 was in a config file
// (the user's or the workspace's), not in the per-workspace file `yolo loopholes enable` wrote.
func legacySwitchInConfig(c legacyCase, name string) bool {
	_, inFile := c.WorkspaceFile[name]
	return !inFile
}

// legacyGrantWhy names the legacy spelling a grant maps from.
func legacyGrantWhy(g loopholedecl.Grant) string {
	switch {
	case g.LegacySetting != "":
		return "settings." + g.LegacySetting + " = true"
	case g.LegacyValues != "":
		return "enabled, settings." + g.LegacyValues
	default:
		return "enabled"
	}
}

func orNone(s string) string {
	if s == "" {
		return "absent"
	}
	return s
}

func toStrings(v []any) []string {
	out := make([]string, 0, len(v))
	for _, x := range v {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func sameSet(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return reflect.DeepEqual(x, y) || (len(x) == 0 && len(y) == 0)
}

// normalizeJSON round-trips v through JSON, so an int and a float64 of one number compare equal.
func normalizeJSON(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// legacyGlob reports whether s matches pattern, where "*" matches any run of characters, "/"
// included, and every other character itself.
func legacyGlob(pattern, s string) bool {
	re := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
	return regexp.MustCompile(re).MatchString(s)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// generatedLegacyCases reads v0151-generated.json.
func generatedLegacyCases(t *testing.T) []legacyCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(legacyCompatDir, "v0151-generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw []struct {
		ID            string          `json:"id"`
		User          json.RawMessage `json:"user"`
		Workspace     json.RawMessage `json:"workspace"`
		WorkspaceFile map[string]bool `json:"workspace_file"`
		V0151         *legacyMeaning  `json:"v0151"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	out := make([]legacyCase, 0, len(raw))
	for _, r := range raw {
		r := r
		out = append(out, legacyCase{ID: r.ID, WorkspaceFile: r.WorkspaceFile, V0151: r.V0151,
			write: func(t *testing.T, base string) {
				home := filepath.Join(base, "home")
				lcMust(t, os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755))
				lcMust(t, os.MkdirAll(filepath.Join(home, "reloc"), 0o755))
				lcMust(t, os.MkdirAll(filepath.Join(base, "ws"), 0o755))
				if len(r.User) > 0 {
					lcMust(t, os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
						[]byte(strings.ReplaceAll(string(r.User), "@HOME@", home)), 0o644))
				}
				if len(r.Workspace) > 0 {
					lcMust(t, os.WriteFile(filepath.Join(base, "ws", "yolo-jail.jsonc"),
						[]byte(strings.ReplaceAll(string(r.Workspace), "@HOME@", home)), 0o644))
				}
			}})
	}
	return out
}

// realisticLegacyCases reads realistic/<case>/ and v0151-realistic.json.
func realisticLegacyCases(t *testing.T) []legacyCase {
	t.Helper()
	root := filepath.Join(legacyCompatDir, "realistic")
	data, err := os.ReadFile(filepath.Join(legacyCompatDir, "v0151-realistic.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v0151 map[string]struct {
		V0151   *legacyMeaning `json:"v0151"`
		Refused string         `json:"refused"`
	}
	if err := json.Unmarshal(data, &v0151); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []legacyCase
	for _, e := range ents {
		var spec struct {
			About           string            `json:"about"`
			CurrentOnly     bool              `json:"current_only"`
			Links           map[string]string `json:"links"`
			Dirs            []string          `json:"dirs"`
			Renames         map[string]string `json:"renames"`
			WorkspaceFile   map[string]bool   `json:"workspace_file"`
			ExpectEnabled   []string          `json:"expect_enabled"`
			ExpectOff       []string          `json:"expect_off"`
			ExpectGitGrants []string          `json:"expect_git_grants"`
			ExpectRefused   string            `json:"expect_refused"`
		}
		dir := filepath.Join(root, e.Name())
		body, err := os.ReadFile(filepath.Join(dir, "case.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &spec); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		lc := legacyCase{ID: "realistic/" + e.Name(), About: spec.About, WorkspaceFile: spec.WorkspaceFile,
			ExpectEnabled: spec.ExpectEnabled, ExpectOff: spec.ExpectOff, ExpectGitGrants: spec.ExpectGitGrants, ExpectRefused: spec.ExpectRefused}
		if !spec.CurrentOnly {
			got, ok := v0151[e.Name()]
			if !ok || got.V0151 == nil {
				t.Fatalf("%s: no v0.15.1 meaning in v0151-realistic.json (refused: %q); rerun probe/ or mark the case current_only",
					e.Name(), got.Refused)
			}
			lc.V0151 = got.V0151
		}
		src, err := filepath.Abs(dir)
		if err != nil {
			t.Fatal(err)
		}
		lc.write = func(t *testing.T, base string) {
			lcMust(t, os.CopyFS(base, os.DirFS(src)))
			_ = os.Remove(filepath.Join(base, "case.json"))
			for from, to := range spec.Renames {
				lcMust(t, os.Rename(filepath.Join(base, from), filepath.Join(base, to)))
			}
			for link, target := range spec.Links {
				lcMust(t, os.MkdirAll(filepath.Dir(filepath.Join(base, link)), 0o755))
				lcMust(t, os.Symlink(target, filepath.Join(base, link)))
			}
			for _, d := range spec.Dirs {
				lcMust(t, os.MkdirAll(filepath.Join(base, d), 0o755))
			}
		}
		out = append(out, lc)
	}
	return out
}

func lcMust(t *testing.T, err error) {
	if err == nil {
		return
	}
	if t == nil {
		panic(err)
	}
	t.Helper()
	t.Fatal(err)
}
