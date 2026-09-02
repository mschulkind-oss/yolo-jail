package entrypoint

// hostmcp_test.go covers "a pack can install Claude MCP servers on the host" — the gap
// docs/plans/handoff-host-mcp-servers.md reported, plus the three care-required items it
// attached to it.
//
// The gap was a granularity bug, and the tests are shaped around that. Claude Code keeps
// user-scope MCP servers in ~/.claude.json (the claude/config surface), and that surface also
// carries two per-jail trust flags keyed under projects.${workspace}. The host render refused
// any surface mentioning ${workspace} ANYWHERE, so those two keys made the entire file
// unreachable at the host notch — including `mcpServers`, which has nothing to do with any
// workspace. A pack contributing a server lint-passed and silently never landed.
//
// So the tests here assert the three things that fix has to get right at once: the unrelated
// content renders, the pruned keys are NAMED (never silent), and the file's other 30-odd keys
// — this is live agent state, not just config — survive byte-identically.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// embeddedPlaceholderSurface is a surface whose managed layer uses the placeholder as a
// SUBSTRING of a key ("${workspace}/sub") rather than as the whole key, beside an unrelated
// sibling. Built fresh per call so a test can mutate it.
func embeddedPlaceholderSurface() manifest.Surface {
	return manifest.Surface{
		Agent: "probe", Name: "cfg", Path: "~/.probe.json", Codec: "json", Mode: "rmw",
		Managed: map[string]any{
			"keep": true,
			"tree": map[string]any{
				"${workspace}/sub": map[string]any{"flag": true},
			},
		},
	}
}

// mcpOverlayPack is a user's own pack contributing MCP servers to claude/config via
// config-overlay — the exact shape the handoff's reproduction used.
func mcpOverlayPack(t *testing.T, servers map[string]any) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"managed": map[string]any{"mcpServers": servers},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "matt-mcp", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: "claude/config", Raw: raw},
		},
	}}
}

// hostRenderClaude renders the SHIPPED claude pack at the host notch with the given overlay
// packs collected exactly as applyHost does (autonomy=false — the guarded posture).
func hostRenderClaude(t *testing.T, home string, observe bool, contributors ...*packload.Pack) []HostRenderResult {
	t.Helper()
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatalf("embedded claude: %v", err)
	}
	overlays := packoverlay.Collect(append([]*packload.Pack{claude}, contributors...), false, nil)
	results, err := RenderHostPack(claude, home, observe, overlays)
	if err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	return results
}

// resultFor finds one surface's result, failing if it is absent — an absent result IS the
// bug in most of these tests, so it must not read as a pass.
func resultFor(t *testing.T, results []HostRenderResult, surface string) HostRenderResult {
	t.Helper()
	for _, r := range results {
		if r.Surface == surface {
			return r
		}
	}
	t.Fatalf("no result for %s; got %+v", surface, results)
	return HostRenderResult{}
}

// THE GAP ITSELF: a pack contributing `mcpServers` to claude/config must LAND in the real
// ~/.claude.json. Before the prune this surface was refused outright ("uses ${workspace},
// which has no referent on the host") and the file was never even created.
func TestHostMCPServerFromPackLands(t *testing.T) {
	home := t.TempDir()
	pack := mcpOverlayPack(t, map[string]any{
		"tavily": map[string]any{"type": "http", "url": "https://example/mcp"},
	})
	results := hostRenderClaude(t, home, false, pack)

	r := resultFor(t, results, "claude/config")
	if r.Action != "rendered" {
		t.Fatalf("claude/config must RENDER at the host now, not %q — the whole point of the "+
			"prune is that two unrelated ${workspace} keys no longer make ~/.claude.json "+
			"off-limits", r.Action)
	}
	got := readRenderedJSON(t, home, ".claude.json")
	servers, ok := got["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers absent from the host render:\n%#v", got)
	}
	entry, ok := servers["tavily"].(map[string]any)
	if !ok {
		t.Fatalf("the pack's server is absent:\n%#v", servers)
	}
	if entry["url"] != "https://example/mcp" {
		t.Errorf("the server's url did not land: %#v", entry)
	}
}

// The pruned ${workspace}-keyed keys must be NAMED in the result, and must NOT reach the
// file. Naming them is the never-silent half: the surface rendering is not a licence for part
// of a pack's declaration to vanish without a word.
func TestHostMCPPrunedKeysAreNamedAndAbsent(t *testing.T) {
	home := t.TempDir()
	results := hostRenderClaude(t, home, false,
		mcpOverlayPack(t, map[string]any{"tavily": map[string]any{"url": "https://example/mcp"}}))

	r := resultFor(t, results, "claude/config")
	joined := strings.Join(r.Pruned, " ")
	for _, want := range []string{
		"projects.${workspace}.hasTrustDialogAccepted",
		"projects.${workspace}.enableAllProjectMcpServers",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("pruned key %q must be reported by name; got %v", want, r.Pruned)
		}
	}

	// And nothing workspace-shaped reached the file. The literal placeholder is the worst
	// case (plausible-looking and completely inert), and an EMPTY "projects" object is the
	// subtler one — a key yolo asserted for no reason.
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "${workspace}") {
		t.Errorf("the literal placeholder reached the user's real home:\n%s", data)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if _, present := got["projects"]; present {
		t.Errorf("an empty `projects` object was asserted into the user's file — a parent left "+
			"empty by the prune must be dropped with it:\n%s", data)
	}
}

// A surface whose layers are ENTIRELY ${workspace}-keyed is skipped — but with a reason that
// names what was pruned, not a bare "uses ${workspace}". With no overlay contributing
// anything, claude/config is exactly that surface, so this also pins that the fix did not
// simply start writing per-jail trust flags into real homes.
func TestHostConfigSkippedWhenOnlyWorkspaceKeyed(t *testing.T) {
	home := t.TempDir()
	results := hostRenderClaude(t, home, false) // no contributors

	r := resultFor(t, results, "claude/config")
	if !strings.HasPrefix(r.Action, "skipped") {
		t.Fatalf("with nothing but ${workspace} keys the surface must be SKIPPED, got %q", r.Action)
	}
	if len(r.Pruned) == 0 {
		t.Error("a skip must name the pruned keys — a bare \"uses ${workspace}\" is the " +
			"message this change exists to remove")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Errorf("a skipped surface must not create the file (err=%v)", err)
	}
}

// realisticClaudeJSON is a ~32-key ~/.claude.json in the shape a real host carries: live
// agent state (startup counters, onboarding flags, cached release notes, per-project history)
// alongside the one key yolo manages. The handoff asked for exactly this test, and the reason
// is blast radius: this file is not settings.json, and a bug in an RMW writer here loses a
// user's history rather than a preference.
const realisticClaudeJSON = `{
  "numStartups": 417,
  "installMethod": "native",
  "autoUpdates": true,
  "theme": "dark",
  "verbose": false,
  "editorMode": "normal",
  "autoCompactEnabled": true,
  "hasSeenTasksHint": true,
  "queuedCommandUpHintCount": 3,
  "diffTool": "auto",
  "customApiKeyResponses": {"approved": ["abc123"], "rejected": []},
  "tipsHistory": {"new-user-warmup": 12, "shift-enter": 40},
  "memoryUsageCount": 9,
  "promptQueueUseCount": 21,
  "todoFeatureEnabled": true,
  "messageIdempotencyKey": "idem-9f8a",
  "cachedChangelog": "## 2.0.1\n- fixes\n",
  "changelogLastFetched": 1754000000000,
  "fallbackAvailableWarningThreshold": 0.2,
  "subscriptionNoticeCount": 2,
  "hasAvailableSubscription": true,
  "lastReleaseNotesSeen": "2.0.1",
  "userID": "user-abcdef",
  "firstStartTime": "2026-01-02T03:04:05.000Z",
  "oauthAccount": {"accountUuid": "uuid-1", "emailAddress": "me@example.com"},
  "isQualifiedForDataSharing": false,
  "shiftEnterKeyBindingInstalled": true,
  "hasCompletedOnboarding": true,
  "lastOnboardingVersion": "2.0.0",
  "recommendedSubscription": "max",
  "mcpServers": {"handAdded": {"command": "/usr/local/bin/mine", "args": ["--serve"]}},
  "projects": {
    "/home/me/work/api": {
      "allowedTools": ["Bash(git:*)"],
      "history": [{"display": "fix the tests", "pastedContents": {}}],
      "hasTrustDialogAccepted": true,
      "projectOnboardingSeenCount": 4
    },
    "/home/me/work/web": {
      "allowedTools": [],
      "history": [],
      "hasTrustDialogAccepted": true
    }
  }
}`

// THE ROUND-TRIP GUARANTEE. A realistic multi-key ~/.claude.json must come back identical
// apart from the asserted key: every unrelated top-level key present with its exact value,
// the user's OTHER projects untouched (including history), and no key invented.
//
// Compared structurally rather than byte-wise on purpose — the writer normalizes to indent-2
// JSON, so raw bytes differ by whitespace while the DATA must not. Whitespace is not what a
// user loses sleep over; a dropped history array is.
func TestHostClaudeJSONRoundTripsUnrelatedKeys(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(realisticClaudeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	var before map[string]any
	if err := json.Unmarshal([]byte(realisticClaudeJSON), &before); err != nil {
		t.Fatal(err)
	}

	hostRenderClaude(t, home, false,
		mcpOverlayPack(t, map[string]any{"tavily": map[string]any{"url": "https://example/mcp"}}))

	var after map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("the render produced unparseable JSON: %v\n%s", err, data)
	}

	// Every key EXCEPT the two yolo touches must be byte-identical by value.
	touched := map[string]bool{"mcpServers": true}
	for k, want := range before {
		if touched[k] {
			continue
		}
		got, present := after[k]
		if !present {
			t.Errorf("key %q was DROPPED from the user's ~/.claude.json — rmw must touch only "+
				"declared keys", k)
			continue
		}
		if !sameJSON(got, want) {
			wb, _ := json.Marshal(want)
			gb, _ := json.Marshal(got)
			t.Errorf("key %q changed:\n  before: %s\n  after:  %s", k, wb, gb)
		}
	}
	// No key invented (the empty-`projects` hazard, generalized).
	for k := range after {
		if _, expected := before[k]; !expected && !touched[k] {
			t.Errorf("the render INVENTED top-level key %q, which the user never had", k)
		}
	}
	// The user's own projects — including history — survive intact. Stated separately
	// because `projects` is the key the pruned branch lived under, so it is the one most
	// likely to be collateral.
	projects, ok := after["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects must survive as an object:\n%s", data)
	}
	if len(projects) != 2 {
		t.Errorf("want the user's 2 projects, got %d: %v", len(projects), projects)
	}
	api, _ := projects["/home/me/work/api"].(map[string]any)
	if hist, _ := api["history"].([]any); len(hist) != 1 {
		t.Errorf("the user's project history was lost: %#v", api)
	}
}

// A SECOND --assert is byte-identical: the render is idempotent, so re-running it does not
// churn the user's file (and `git diff` on a dotfiles repo stays clean).
func TestHostClaudeJSONSecondAssertIsByteIdentical(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(realisticClaudeJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := mcpOverlayPack(t, map[string]any{
		"tavily": map[string]any{"type": "http", "url": "https://example/mcp"},
	})
	hostRenderClaude(t, home, false, pack)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hostRenderClaude(t, home, false, pack)
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("a second --assert changed the file:\n--- first\n%s\n--- second\n%s", first, second)
	}
}

// A ${VAR} in pack content reaches the host LITERAL and yolo says nothing about it. Both
// halves are the point, and both are a deliberate 2026-08-03 reversal:
//
//   - LITERAL, because a host render resolves no variables and (since the jail-side
//     interpolation was removed) neither notch does. Whoever launches the server resolves it.
//   - UNREPORTED, because the warning that used to fire here recommended "put the value in
//     the file directly" — inlining a live credential into a file a pack may carry — and was
//     surface-wide, so it flagged the `env` case (where a literal ${VAR} is exactly right)
//     with the same words as the `url` case.
func TestHostMCPUnresolvedVarIsWrittenLiterallyAndNotReported(t *testing.T) {
	home := t.TempDir()
	url := "https://mcp.tavily.com/mcp/?tavilyApiKey=${TAVILY_API_KEY}"
	// observe=false: the file must actually be WRITTEN, since the second half of this test
	// asserts what reached the user's config, not what a preview said.
	results := hostRenderClaude(t, home, false, mcpOverlayPack(t, map[string]any{
		"tavily": map[string]any{"type": "http", "url": url},
	}))
	r := resultFor(t, results, "claude/config")
	// Nothing in the report may name the variable — that is the removed warning.
	for _, field := range []string{r.Action, strings.Join(r.Pruned, " "), strings.Join(r.Overwrites, " ")} {
		if strings.Contains(field, "TAVILY_API_KEY") || strings.Contains(field, "LITERALLY") {
			t.Errorf("the ${VAR} warning is gone by ruling; report still mentions it: %q", field)
		}
	}
	// And the file carries the reference VERBATIM — not resolved, and not blanked.
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read rendered config: %v", err)
	}
	if !strings.Contains(string(data), "${TAVILY_API_KEY}") {
		t.Errorf("the ${VAR} must reach the file LITERAL, got:\n%s", data)
	}
}

// THE ONE-WAY DOOR, detection half. A pre-existing hand-added server that the pack's own
// entry would MANGLE (leaving the user's `type`+`url` merged into a stdio entry, which is a
// broken server) is reported as an EntryLoss on the first apply — the signal yolo host apply
// gates its confirmation on.
func TestHostMCPFirstApplyReportsEntryLoss(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	// The user's working host entry: the http transport with the key in the url.
	if err := os.WriteFile(path, []byte(`{"numStartups":5,"mcpServers":{`+
		`"tavily":{"type":"http","url":"https://mcp.tavily.com/mcp/?tavilyApiKey=SECRET"}}}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	// The pack declares the SAME server in the stdio form — every key an add, so leaf-level
	// overwrite detection sees nothing, yet the merged entry carries both transports.
	pack := mcpOverlayPack(t, map[string]any{
		"tavily": map[string]any{
			"command": "npx", "args": []any{"-y", "tavily-mcp@latest"},
			"env": map[string]any{"TAVILY_API_KEY": "${TAVILY_API_KEY}"},
		},
	})

	// OBSERVE reports it and writes nothing — the user gets the information before any prompt.
	results := hostRenderClaude(t, home, true, pack)
	r := resultFor(t, results, "claude/config")
	if !r.FirstApply {
		t.Error("a home yolo has never asserted must report FirstApply — it is what " +
			"distinguishes data loss from the opted-in wholesale-regeneration policy")
	}
	if len(r.EntryLosses) == 0 {
		t.Fatalf("a merge that leaves the user's entry half-intact must be reported as an "+
			"EntryLoss; got %+v", r)
	}
	if !strings.Contains(strings.Join(r.EntryLosses, " "), "tavily") {
		t.Errorf("the loss must NAME the entry, got %v", r.EntryLosses)
	}
	before, _ := os.ReadFile(path)
	if !strings.Contains(string(before), "SECRET") {
		t.Error("observe must write nothing")
	}

	// After an ASSERT, the same collision is no longer a first apply: the user has opted in,
	// so wholesale regeneration is the documented policy rather than a one-way door.
	hostRenderClaude(t, home, false, pack)
	results = hostRenderClaude(t, home, true, pack)
	if r := resultFor(t, results, "claude/config"); r.FirstApply {
		t.Error("after an assert wrote a provenance record, this home is no longer a first " +
			"apply — otherwise the confirmation would fire forever")
	}
}

// A CLEAN home reports no loss at all, so the confirmation never fires on it. This is the
// property that keeps the gate from becoming noise: a prompt on every run trains people to
// hit `y` without reading, which costs more than it protects.
func TestHostMCPCleanHomeReportsNoLoss(t *testing.T) {
	home := t.TempDir()
	results := hostRenderClaude(t, home, true, mcpOverlayPack(t, map[string]any{
		"tavily": map[string]any{"type": "http", "url": "https://example/mcp"},
	}))
	r := resultFor(t, results, "claude/config")
	if !r.FirstApply {
		t.Error("an empty home IS a first apply")
	}
	if len(r.EntryLosses) != 0 {
		t.Errorf("nothing exists to lose in an empty home; got %v", r.EntryLosses)
	}
}

// WHOLESALE REGENERATION, and its two casualties named distinctly. A user's hand-added
// server that config does not declare is DROPPED — the maintainer's ruling ("if you manage
// mcpServers through yolo, you give up `claude mcp add`") makes that correct policy, and
// correct policy still has to be reported. A server config DOES declare, differently, is
// REPLACED. The two words are different because they are different mistakes to have made.
func TestHostMCPWholesaleTableReportsDroppedAndReplaced(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{`+
		`"handAdded":{"command":"/usr/local/bin/mine"},`+
		`"tavily":{"type":"http","url":"https://old?k=SECRET"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	results := hostRenderClaude(t, home, true, mcpOverlayPack(t, map[string]any{
		"tavily": map[string]any{"type": "http", "url": "https://new/mcp"},
	}))
	joined := strings.Join(resultFor(t, results, "claude/config").EntryLosses, " | ")
	if !strings.Contains(joined, "handAdded") || !strings.Contains(joined, "dropped") {
		t.Errorf("an undeclared server must be reported as DROPPED; got %q", joined)
	}
	if !strings.Contains(joined, "tavily") || !strings.Contains(joined, "replaced") {
		t.Errorf("a redeclared server must be reported as REPLACED; got %q", joined)
	}
}

// An IDENTICAL re-assert is not a loss. The table is rewritten wholesale either way, but
// nothing changed, so there is nothing to report — the property that makes a second apply
// quiet and keeps the confirmation from firing on a no-op.
func TestHostMCPIdenticalEntryIsNotALoss(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(
		`{"mcpServers":{"tavily":{"type":"http","url":"https://example/mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	results := hostRenderClaude(t, home, true, mcpOverlayPack(t, map[string]any{
		"tavily": map[string]any{"type": "http", "url": "https://example/mcp"},
	}))
	if r := resultFor(t, results, "claude/config"); len(r.EntryLosses) != 0 {
		t.Errorf("re-asserting an identical entry loses nothing; got %v", r.EntryLosses)
	}
}

// THE HYBRID-RECORD BUG, pinned. A user's http entry and a pack's stdio entry for the SAME
// server name must NOT deep-merge into a record carrying both transports — that is a
// "nothing was lost" merge that silently breaks the server, and it is what wholesale
// replacement exists to prevent. The user's `type`/`url` must be GONE, not kept alongside.
func TestHostMCPReplacesRatherThanMergingTransports(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"numStartups":9,"mcpServers":{`+
		`"tavily":{"type":"http","url":"https://mcp.tavily.com/mcp/?tavilyApiKey=SECRET"}}}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	hostRenderClaude(t, home, false, mcpOverlayPack(t, map[string]any{
		"tavily": map[string]any{
			"command": "npx", "args": []any{"-y", "tavily-mcp@latest"},
		},
	}))
	got := readRenderedJSON(t, home, ".claude.json")
	servers, _ := got["mcpServers"].(map[string]any)
	entry, ok := servers["tavily"].(map[string]any)
	if !ok {
		t.Fatalf("the entry is missing entirely: %#v", servers)
	}
	if _, stillThere := entry["url"]; stillThere {
		t.Errorf("the user's `url` survived INSIDE the replaced entry — that is the "+
			"two-transport hybrid record no client can use: %#v", entry)
	}
	if _, stillThere := entry["type"]; stillThere {
		t.Errorf("the user's `type` survived inside the replaced entry: %#v", entry)
	}
	if entry["command"] != "npx" {
		t.Errorf("the pack's declaration must be what remains: %#v", entry)
	}
	// The rest of the file is still untouched — replacement is scoped to the table.
	if got["numStartups"] != float64(9) {
		t.Errorf("wholesale table replacement must not reach outside the table: %#v", got)
	}
}

// A SCALAR overwrite is NOT an EntryLoss. The split is load-bearing: a changed scalar is
// named, reversible, and reported as an ordinary ⚠ — gating a confirmation on it would fire
// the prompt on nearly every first apply (claude/settings alone flips autoUpdaterStatus).
func TestHostScalarOverwriteIsNotAnEntryLoss(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings,
		[]byte(`{"preferences":{"autoUpdaterStatus":"enabled"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	results := hostRenderClaude(t, home, true)
	r := resultFor(t, results, "claude/settings")
	if len(r.Overwrites) == 0 {
		t.Error("a differing managed scalar must still be reported as an ordinary overwrite")
	}
	if len(r.EntryLosses) != 0 {
		t.Errorf("a scalar flip is not an entry loss — it is named and reversible; got %v",
			r.EntryLosses)
	}
}

// The prune is CONTAINS, not equals: agentcfg.SubstituteWorkspace rewrites only keys that
// EQUAL the placeholder, so a key like "${workspace}/sub" would otherwise survive with the
// literal text in it. Pinned because the safe behavior here differs from the substitution it
// might be assumed to mirror.
func TestPruneWorkspaceKeyedHandlesEmbeddedPlaceholder(t *testing.T) {
	s := embeddedPlaceholderSurface()
	pruned, keys := pruneWorkspaceKeyed(s)
	if got := strings.Join(keys, ","); !strings.Contains(got, "${workspace}/sub") {
		t.Errorf("a key CONTAINING the placeholder must be pruned; got %v", keys)
	}
	m, _ := pruned.Managed.(map[string]any)
	if _, present := m["keep"]; !present {
		t.Errorf("the unrelated sibling must survive: %#v", m)
	}
	blob, _ := json.Marshal(pruned.Managed)
	if strings.Contains(string(blob), "${workspace}") {
		t.Errorf("no placeholder text may survive the prune: %s", blob)
	}
}

// A surface declaring an EMPTY object stays empty rather than being read as "fully pruned" —
// nothing pruned it, so there is nothing to report and the declaration is honored as written.
func TestPruneWorkspaceKeyedKeepsDeclaredEmptyObject(t *testing.T) {
	s := embeddedPlaceholderSurface()
	m, _ := s.Managed.(map[string]any)
	m["emptyOnPurpose"] = map[string]any{}
	pruned, keys := pruneWorkspaceKeyed(s)
	for _, k := range keys {
		if strings.Contains(k, "emptyOnPurpose") {
			t.Errorf("a declared-empty object was not pruned by anything: %v", keys)
		}
	}
	got, _ := pruned.Managed.(map[string]any)
	if _, present := got["emptyOnPurpose"]; !present {
		t.Errorf("a declared-empty object must survive: %#v", got)
	}
}

// --- the JAIL half of §3: ${VAR} in `url` -------------------------------------------
//
// Interpolation used to cover `env` values ONLY, which made the http/sse transports
// unusable with any secret: the canonical remote-MCP form puts the credential in the query
// string, and that landed verbatim so the server 401'd with nothing said. These pin the
// widened field set from the loader the jail boot actually calls.

// mcpEnv builds an Env with one MCP server declared and the given vars resolvable.
func mcpEnv(serversJSON string, vars map[string]string) (*Env, *strings.Builder) {
	var errw strings.Builder
	all := map[string]string{"YOLO_MCP_SERVERS": serversJSON}
	for k, v := range vars {
		all[k] = v
	}
	e := NewEnv(all)
	e.Stderr = &errw
	return e, &errw
}

// ${VAR} INTERPOLATION IS GONE FROM THE JAIL TOO (2026-08-03 ruling). yolo writes the
// reference verbatim into every field, and the agent that launches the server resolves it
// from its own environment — where env_sources values already are, because
// hydrateEnvFromUserEnvFile os.Setenv's them before any generator runs.
//
// This replaces three tests that pinned the opposite (url expanded, headers expanded, an
// unresolved url warned). They are not weakened, they are REVERSED: the reason is structural
// and recorded at length in mcp.go — an interpolated value has no provenance LAYER, and
// sourcing config content from process env at render time makes the rendered bytes depend on
// the ambient environment of whoever ran the render.
//
// One table covering every field the old code touched, plus the two it deliberately never
// did, so "yolo does not interpolate ANY field" is asserted rather than implied by three
// separate examples.
func TestJailMCPNeverInterpolatesAnyField(t *testing.T) {
	e, errw := mcpEnv(
		`{"tavily":{"type":"http","url":"https://x/?k=${SECRET}",`+
			`"headers":{"Authorization":"Bearer ${SECRET}"},`+
			`"env":{"KEY":"${SECRET}"},`+
			`"command":"${SECRET}","args":["${SECRET}"]}}`,
		map[string]string{"SECRET": "tvly-resolved-value"})
	got := prismMap(e.LoadMCPServers())
	entry, _ := got["tavily"].(map[string]any)
	if entry == nil {
		t.Fatalf("server missing: %#v", got)
	}
	if entry["url"] != "https://x/?k=${SECRET}" {
		t.Errorf("url must stay literal, got %v", entry["url"])
	}
	if h, _ := entry["headers"].(map[string]any); h["Authorization"] != "Bearer ${SECRET}" {
		t.Errorf("headers must stay literal, got %v", entry["headers"])
	}
	if ev, _ := entry["env"].(map[string]any); ev["KEY"] != "${SECRET}" {
		t.Errorf("env must stay literal, got %v", entry["env"])
	}
	if entry["command"] != "${SECRET}" {
		t.Errorf("command must stay literal, got %v", entry["command"])
	}
	if args, _ := entry["args"].([]any); len(args) != 1 || args[0] != "${SECRET}" {
		t.Errorf("args must stay literal, got %v", entry["args"])
	}
	// THE SECRET MUST NOT APPEAR ANYWHERE. This is the assertion that would have caught the
	// old behavior no matter which field carried the reference.
	if blob := fmt.Sprintf("%v", got); strings.Contains(blob, "tvly-resolved-value") {
		t.Errorf("a resolvable value leaked into the rendered config: %s", blob)
	}
	// And no warning: yolo has no opinion about a reference it does not resolve.
	if errw.String() != "" {
		t.Errorf("yolo must say nothing about ${VAR} now, got %q", errw.String())
	}
}

// An UNDEFINED variable is treated identically to a defined one — literal, no warning. Pinned
// separately because the old code's whole justification was that this case "silently 401s",
// and the answer is now that resolution is not yolo's job in either direction.
func TestJailMCPUndefinedVarIsAlsoJustLiteral(t *testing.T) {
	e, errw := mcpEnv(`{"tavily":{"type":"http","url":"https://x/?k=${MISSING_KEY}"}}`, nil)
	got := prismMap(e.LoadMCPServers())
	entry, _ := got["tavily"].(map[string]any)
	if entry["url"] != "https://x/?k=${MISSING_KEY}" {
		t.Errorf("must stay literal, got %v", entry["url"])
	}
	if errw.String() != "" {
		t.Errorf("no warning for an undefined var either, got %q", errw.String())
	}
}

// --- UNIFORMITY: every agent's MCP table is a wholesale table at the host ---------------
//
// The whole point of the handoff was that claude was "the odd one out purely because of
// where Claude Code chose to store user MCP config". Once claude renders, host MCP
// management should be uniform — and uniform means the same WRITE SEMANTICS, not just that a
// file gets written.

// Every shipped pack surface that derives an MCP/LSP table must be recognized as a wholesale
// table at the host notch. This is the regression test for a real trap: codex's and
// opencode's derives implement `omitEmpty` by returning {} when no servers are configured, so
// probing the derive with EMPTY tables reported no table keys for exactly those two — they
// would have deep-merged while claude and copilot replaced. Invisible in any test that has
// servers configured, and a silently different policy per agent.
func TestHostTableKeysUniformAcrossShippedPacks(t *testing.T) {
	want := map[string]string{
		"claude/config":   "mcpServers",
		"copilot/mcp":     "mcpServers",
		"copilot/lsp":     "lspServers",
		"agy/mcp":         "mcpServers",
		"codex/config":    "mcp_servers", // omitEmpty — the trap
		"opencode/config": "mcp",         // omitEmpty — the trap
	}
	found := map[string]bool{}
	for _, name := range EmbeddedPackNames() {
		p, err := embeddedPack(name)
		if err != nil {
			t.Fatalf("embedded %s: %v", name, err)
		}
		surfaces, _ := p.SurfacesFor(false)
		for _, s := range surfaces {
			id := s.Agent + "/" + s.Name
			keys := hostTableKeys(p, s)
			wantKey, expected := want[id]
			if !expected {
				continue
			}
			found[id] = true
			if !contains(keys, wantKey) {
				t.Errorf("%s must report %q as a wholesale table at the host — otherwise it is "+
					"deep-merged while other agents' MCP tables are replaced, which is the "+
					"per-agent inconsistency this fix exists to remove. got %v", id, wantKey, keys)
			}
		}
	}
	for id := range want {
		if !found[id] {
			t.Errorf("surface %s was never visited — did a pack drop it?", id)
		}
	}
}
