package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// promoteWorld is the host-side world one promotion runs in: a scratch HOME carrying a user
// config, and a workspace whose prism sidecars hold the captures.
//
// YOLO_VERSION is cleared deliberately — promote REFUSES in a jail, and the dev jail this
// suite usually runs in would otherwise take that branch for every test. The refusal itself
// is pinned separately (TestPromoteRefusesInsideAJail), which is what keeps clearing the
// variable here from hiding it.
type promoteWorld struct {
	t    *testing.T
	home string
	ws   string
}

// newPromoteWorld builds the world with the given `packs` config value (raw JSON).
func newPromoteWorld(t *testing.T, packsJSON string) *promoteWorld {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USE_PROFILES", "")
	ws := t.TempDir()
	orig := prismSidecarDir
	prismSidecarDir = func() string { return filepath.Join(ws, ".yolo", "prism") }
	t.Cleanup(func() { prismSidecarDir = orig })
	if err := os.MkdirAll(prismSidecarDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":`+packsJSON+`}`)
	return &promoteWorld{t: t, home: home, ws: ws}
}

// capture seeds one surface's two sidecars.
func (w *promoteWorld) capture(agent, name, overlayJSON, lastRender string) {
	w.t.Helper()
	writeSidecar(w.t, prismSidecarDir(), agent, name, overlayJSON, lastRender)
}

// pack writes a local pack directory with the given manifest and returns its file:// entry.
func (w *promoteWorld) pack(name, manifest string) string {
	w.t.Helper()
	dir := filepath.Join(w.home, "packs", name)
	writeFile(w.t, filepath.Join(dir, "pack.json"), manifest)
	return `{"source":"file://` + dir + `","name":"` + name + `"}`
}

// run invokes `yolo config promote …` and returns stdout, stderr and the exit code.
func (w *promoteWorld) run(args ...string) (string, string, int) {
	w.t.Helper()
	var out, errw bytes.Buffer
	rc := configRunW(append([]string{"promote"}, args...), &out, &errw)
	return out.String(), errw.String(), rc
}

// dispositionOf finds one key's line in the text report and returns the disposition token.
func dispositionOf(t *testing.T, report, key string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != key {
			continue
		}
		return fields[1]
	}
	t.Fatalf("no line for key %q in:\n%s", key, report)
	return ""
}

// overlayFor reads back a capture sidecar.
func (w *promoteWorld) overlayFor(agent, name string) string {
	w.t.Helper()
	data, err := os.ReadFile(prismOverlayPath(agent, name))
	if err != nil {
		w.t.Fatal(err)
	}
	return string(data)
}

// §5.5: promote is a HOST-side verb. Its destinations are under the host's
// ~/.config/yolo-jail/, which no jail may write — a pack manifest is an input to
// composition, so a jail that could edit one could grant itself a host file on the next
// boot. In-jail it must refuse and name the host command, the mirror of the host-side
// refusal on capture/reset.
//
// It refuses the PLAN too, deliberately: the jail-side request channel is designed and
// unbuilt ([OQ-CO5]), and a read-only half that works in here is how a verb acquires a
// second front door nobody ruled on.
func TestPromoteRefusesInsideAJail(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings", `{"model":"mine"}`, `{"model":"theirs"}`)
	withLocalSurfaces(t) // surfacesAreLocal()==true: the jail that owns the workspace

	before := w.overlayFor("claude", "settings")
	for _, args := range [][]string{{"claude"}, {"claude", "--plan"}, {"claude", "--accept-promotion"}} {
		_, errw, rc := w.run(args...)
		if rc == 0 {
			t.Errorf("promote %v in-jail: rc=0, want a refusal", args)
		}
		if !strings.Contains(errw, "HOST-side") {
			t.Errorf("promote %v: refusal does not say where it must run:\n%s", args, errw)
		}
	}
	if after := w.overlayFor("claude", "settings"); after != before {
		t.Errorf("the in-jail refusal touched the capture overlay:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".config", "yolo-jail", "local")); err == nil {
		t.Error("the in-jail refusal created the local pack dir")
	}
}

// §5.2 step 2: a capture identical to yolo's own last render is REDUNDANT — the layers
// already produce it — and promote drops it rather than declaring a value that changes
// nothing. It reads the comparison `config diff` prints (overlayKeyStates) rather than
// re-deriving one; a second implementation of "is this capture yolo's own output?" is what
// made that answer wrong for TOML surfaces until its codec fix.
func TestPromotePlanDropsARedundantCapture(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings",
		`{"model":"same","autoMemoryEnabled":true}`,
		`{"model":"same"}`)

	out, errw, rc := w.run("claude", "--plan")
	if rc != 0 {
		t.Fatalf("rc=%d: %s", rc, errw)
	}
	if got := dispositionOf(t, out, "model"); got != promotionRedundant {
		t.Errorf("model = %s, want %s (identical to the last render):\n%s", got, promotionRedundant, out)
	}
	if got := dispositionOf(t, out, "autoMemoryEnabled"); got != promotionPromotable {
		t.Errorf("autoMemoryEnabled = %s, want %s:\n%s", got, promotionPromotable, out)
	}
}

// §5.2 step 2's other drop: a key an owning layer overrides where it ALREADY SITS is dead,
// and promotion moves it LOWER in the fold, so the move cannot help it.
//
// claude/settings' managed layer (the jail's autonomous posture) asserts
// `skipDangerousModePermissionPrompt` and the four `permissions` leaves. The capture below
// holds a different value for one of them, which is exactly the stale-managed-value case
// narrowOverlay exists to clean up — and the same run must keep a live sibling
// (`permissions.ask`, which managed does not assert) promotable.
func TestPromotePlanDropsAKeyTheManagedLayerOverrides(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings",
		`{"skipDangerousModePermissionPrompt":false,"permissions":{"ask":["Bash"]},"model":"mine"}`,
		`{"model":"theirs"}`)

	out, errw, rc := w.run("claude", "--plan")
	if rc != 0 {
		t.Fatalf("rc=%d: %s", rc, errw)
	}
	if got := dispositionOf(t, out, "skipDangerousModePermissionPrompt"); got != promotionDead {
		t.Errorf("skipDangerousModePermissionPrompt = %s, want %s (managed overrides it):\n%s",
			got, promotionDead, out)
	}
	// The granularity half: `permissions.ask` is a leaf managed does not hold, so the key
	// reaches the file and IS promotable. A blanket top-level drop would fail here.
	if got := dispositionOf(t, out, "permissions"); got != promotionPromotable {
		t.Errorf("permissions = %s, want %s (`ask` is not a managed leaf):\n%s",
			got, promotionPromotable, out)
	}
}

// §5.3: a credential-named key is REFUSED, never redacted, and `--force` is per key and
// named in the output. The measured case is four levels down — this repo's own jail has
// `mcp.tavily.environment.TAVILY_API_KEY` in a capture overlay — so the check has to walk
// the subtree, and the refusal has to name the path that tripped it or a user looking at a
// top-level key cannot act on it.
func TestPromotePlanRefusesACredentialNamedKeyAndNamesTheForce(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings",
		`{"env":{"TAVILY_API_KEY":"tvly-dev-SECRETVALUE","EDITOR":"cat"}}`, `{}`)

	out, errw, rc := w.run("claude", "--plan")
	if rc != 0 {
		t.Fatalf("rc=%d: %s", rc, errw)
	}
	if got := dispositionOf(t, out, "env"); got != promotionSensitive {
		t.Errorf("env = %s, want %s:\n%s", got, promotionSensitive, out)
	}
	if !strings.Contains(out, "env.TAVILY_API_KEY") {
		t.Errorf("the refusal does not name the key path that tripped it:\n%s", out)
	}
	if !strings.Contains(out, "--force env") {
		t.Errorf("the refusal does not name the per-key override:\n%s", out)
	}

	// --force NAMING THE KEY is the way past, and the override is reported rather than
	// producing a clean-looking promotion.
	out, _, rc = w.run("claude", "--plan", "--force", "env")
	if rc != 0 {
		t.Fatalf("--force rc=%d", rc)
	}
	if got := dispositionOf(t, out, "env"); got != promotionPromotable {
		t.Errorf("env with --force = %s, want %s:\n%s", got, promotionPromotable, out)
	}
	if !strings.Contains(out, "overridden by --force") {
		t.Errorf("a forced promotion must say so:\n%s", out)
	}
	// A key the flag does not name keeps its refusal: --force is per key, not a mode.
	w.capture("claude", "settings",
		`{"env":{"TAVILY_API_KEY":"tvly-dev-SECRETVALUE"},"apiKeyHelper":"/bin/creds"}`, `{}`)
	out, _, _ = w.run("claude", "--plan", "--force", "env")
	if got := dispositionOf(t, out, "apiKeyHelper"); got != promotionSensitive {
		t.Errorf("apiKeyHelper = %s, want %s — --force env must not cover it:\n%s",
			got, promotionSensitive, out)
	}
}

// THE PLAN NAMES KEYS AND NEVER PRINTS VALUES (docs/design/report-tiers.md §4.4's first
// forbidden thing). It is load-bearing here rather than inherited: the values are captured
// config, one of them is a live-looking API key in this repo's own jail, and a plan is the
// output most likely to be pasted into a bug report or handed to another agent.
//
// Both views, because they are different writers: the text report and the JSON document.
func TestPromotePlanNeverPrintsACapturedValue(t *testing.T) {
	const secret = "tvly-dev-DO-NOT-PRINT-ME"
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings",
		`{"env":{"TAVILY_API_KEY":"`+secret+`"},"model":"opus-secret-model-name"}`, `{}`)

	text, _, _ := w.run("claude", "--plan")
	doc, _, _ := w.run("claude", "--plan", "--json")
	for view, body := range map[string]string{"text": text, "json": doc} {
		if strings.Contains(body, secret) {
			t.Errorf("the %s plan printed a captured VALUE:\n%s", view, body)
		}
		if strings.Contains(body, "opus-secret-model-name") {
			t.Errorf("the %s plan printed a captured VALUE:\n%s", view, body)
		}
		if !strings.Contains(body, "env") || !strings.Contains(body, "model") {
			t.Errorf("the %s plan lost the key NAMES, which it must carry:\n%s", view, body)
		}
	}
}

// §5.3: a value that is an artifact of the jail it was set in is refused rather than
// rewritten — rewriting means guessing which occurrence of /workspace the agent meant.
func TestPromotePlanRefusesAnEnvironmentBoundValue(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings",
		`{"preferences":{"scratch":"/workspace/.scratch"},"autoMemoryEnabled":true}`, `{}`)

	out, _, rc := w.run("claude", "--plan")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := dispositionOf(t, out, "preferences"); got != promotionEnvironmentBound {
		t.Errorf("preferences = %s, want %s:\n%s", got, promotionEnvironmentBound, out)
	}
	if !strings.Contains(out, "/workspace") {
		t.Errorf("the refusal does not name the marker it found:\n%s", out)
	}
	// A path that merely LOOKS like the marker is not one: /workspaces/other is somebody
	// else's directory, and refusing it would be a refusal the user cannot act on.
	w.capture("claude", "settings", `{"preferences":{"scratch":"/workspaces/other"}}`, `{}`)
	out, _, _ = w.run("claude", "--plan")
	if got := dispositionOf(t, out, "preferences"); got != promotionPromotable {
		t.Errorf("preferences = %s, want %s (/workspaces is not /workspace):\n%s",
			got, promotionPromotable, out)
	}
}

// §5.4 / §11's criterion: promoting a key that would LOSE precedence at its destination
// fails, with the losing layer named — verified by promoting `--to pack:<earlier>` a key a
// later-ordered pack's config-overlay also sets.
//
// And its converse, which is the fact the default destination rests on: the conventional
// local pack folds LAST (config/packs.go's "ORDER IS LOAD-BEARING, AND IT IS LAST"), so the
// same key promoted to `local` outranks that same pack and is promotable. One fixture, two
// destinations — if the check were a constant either way, one of the two would fail.
func TestPromotePrecedenceIsTwoSidedAndLocalFoldsLast(t *testing.T) {
	w := newPromoteWorld(t, `[]`)
	early := w.pack("early", `{"name":"early","contributes":[]}`)
	later := w.pack("zlater", `{"name":"zlater","contributes":[
	  {"kind":"config-overlay","surface":"claude/settings","config":{"managed":{"model":"theirs"}}}]}`)
	writeFile(t, filepath.Join(w.home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude",`+early+`,`+later+`]}`)
	w.capture("claude", "settings", `{"model":"mine"}`, `{}`)

	out, _, rc := w.run("claude", "--plan", "--to", "pack:early")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := dispositionOf(t, out, "model"); got != promotionOutranked {
		t.Errorf("model --to pack:early = %s, want %s:\n%s", got, promotionOutranked, out)
	}
	if !strings.Contains(out, "zlater") {
		t.Errorf("the refusal does not name the pack that would win:\n%s", out)
	}

	out, _, rc = w.run("claude", "--plan", "--to", "local")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := dispositionOf(t, out, "model"); got != promotionPromotable {
		t.Errorf("model --to local = %s, want %s — the local pack folds after every "+
			"configured entry:\n%s", got, promotionPromotable, out)
	}
}

// A config-overlay contributes to a surface a PACK owns (packoverlay's ruling R2), so a
// promotion naming a surface with no owner would declare a contribution nothing folds.
//
// MEASURED CASE: mise/config is core's OWN surface, and this repo's jail carries a captured
// `tools` key on it — so the first plausible promotion anyone runs here is one that would
// silently have delivered nothing.
func TestPromoteRefusesASurfaceNoPackOwns(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("mise", "config", `{"tools":{"neovim":"nightly"}}`, "[tools]\n")

	out, _, rc := w.run("mise", "--plan")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := dispositionOf(t, out, "tools"); got != promotionNoPackOwner {
		t.Errorf("tools = %s, want %s:\n%s", got, promotionNoPackOwner, out)
	}
	if !strings.Contains(out, "yolo's OWN surfaces") {
		t.Errorf("the refusal does not say WHY the surface has no pack owner:\n%s", out)
	}
}

// [OQ-CO10]: promote refuses `--to host` on a surface with no host layer. Exactly two
// shipped surfaces declare `readsHost`; for every other one a host promotion is an edit no
// jail would ever read.
//
// Bound to the PREDICATE — Surface.HasHostLayer — and not to what populates it. That was a
// deliberate choice when the predicate meant "a `reads-host` contribution matched this
// surface's basename", and it is why this test needed no edit on 2026-09-12 when [OQ-CO10]
// moved the declaration onto the surface and the basename match was deleted. What it pins
// is unchanged: the refusal tracks whether the surface HAS a host layer, by whatever
// mechanism gives it one.
func TestPromoteToHostRefusesASurfaceWithNoHostLayer(t *testing.T) {
	w := newPromoteWorld(t, `["claude","codex"]`)
	w.capture("codex", "config", "{\"model\":\"mine\"}", "model = \"theirs\"\n")
	w.capture("claude", "settings", `{"model":"mine"}`, `{}`)

	out, _, rc := w.run("codex", "--plan", "--to", "host")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := dispositionOf(t, out, "model"); got != promotionNoHostLayer {
		t.Errorf("codex/config model --to host = %s, want %s:\n%s", got, promotionNoHostLayer, out)
	}
	// claude/settings HAS one (packs/claude grants reads-host for it), so the same flag on
	// that surface is not refused for this reason — which is what makes the test above a
	// statement about host layers rather than about `--to host`.
	out, _, rc = w.run("claude", "--plan", "--to", "host")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := dispositionOf(t, out, "model"); got == promotionNoHostLayer {
		t.Errorf("claude/settings reported as having no host layer:\n%s", out)
	}
}

// `--plan --json` is the classification as DATA (§5.3): a consumer branches on the
// disposition token rather than on prose, and the document is a projection of the same
// classification the text report prints.
func TestPromotePlanJSONCarriesTheClassification(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings",
		`{"model":"same","autoMemoryEnabled":true}`, `{"model":"same"}`)

	out, errw, rc := w.run("claude", "--plan", "--json")
	if rc != 0 {
		t.Fatalf("rc=%d: %s", rc, errw)
	}
	var doc struct {
		Posture         string `json:"posture"`
		Agent           string `json:"agent"`
		Destination     string `json:"destination"`
		DestinationPath string `json:"destination_path"`
		Counts          struct {
			Promotable int `json:"promotable"`
			Held       int `json:"held"`
		} `json:"counts"`
		Surfaces []struct {
			Surface string `json:"surface"`
			Keys    []struct {
				Key         string `json:"key"`
				Disposition string `json:"disposition"`
				Reason      string `json:"reason"`
			} `json:"keys"`
		} `json:"surfaces"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the plan is not valid JSON (%v):\n%s", err, out)
	}
	if doc.Posture != "plan" || doc.Agent != "claude" || doc.Destination != "local" {
		t.Errorf("document header = %+v", doc)
	}
	if !strings.HasSuffix(doc.DestinationPath, filepath.Join("local", "pack.json")) {
		t.Errorf("destination_path = %q", doc.DestinationPath)
	}
	if doc.Counts.Promotable != 1 || doc.Counts.Held != 1 {
		t.Errorf("counts = %+v, want 1 promotable / 1 held", doc.Counts)
	}
	got := map[string]string{}
	for _, s := range doc.Surfaces {
		for _, k := range s.Keys {
			got[s.Surface+"."+k.Key] = k.Disposition
			if k.Reason == "" {
				t.Errorf("%s.%s carries no reason", s.Surface, k.Key)
			}
		}
	}
	if got["claude/settings.model"] != promotionRedundant ||
		got["claude/settings.autoMemoryEnabled"] != promotionPromotable {
		t.Errorf("dispositions = %v", got)
	}
}

// Misuse is machine-detectable: exit 2, message on stderr, nothing written.
func TestPromoteMisuse(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{}, "needs an agent"},
		{[]string{"claude", "--bogus"}, "unknown flag"},
		{[]string{"claude", "--plan", "--accept-promotion"}, "contradict"},
		{[]string{"claude", "--json"}, "--plan"},
		{[]string{"claude", "--force"}, "there is no blanket form"},
		{[]string{"claude", "--keys", "nosuchkey", "--plan"}, "not captured"},
		{[]string{"user", "--plan"}, "no surface identity"},
		{[]string{"claude", "--surface", "config", "--plan"}, "records no captured edits"},
	}
	for _, c := range cases {
		w := newPromoteWorld(t, `["claude"]`)
		w.capture("claude", "settings", `{"model":"mine"}`, `{}`)
		_, errw, rc := w.run(c.args...)
		if rc != 2 {
			t.Errorf("promote %v: rc=%d, want 2 (stderr: %s)", c.args, rc, errw)
		}
		if !strings.Contains(errw, c.want) {
			t.Errorf("promote %v: stderr %q missing %q", c.args, errw, c.want)
		}
	}
}

// §5.6: no captures for the surface is a NO-OP that SAYS SO — exit 0, not an error and not
// silence. "Nothing happened" and "nothing needed to happen" must be distinguishable.
func TestPromoteWithNoCapturesSaysSo(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings", `{}`, `{}`)

	out, errw, rc := w.run("claude")
	if rc != 0 {
		t.Fatalf("rc=%d: %s", rc, errw)
	}
	if !strings.Contains(out, "no captured keys") && !strings.Contains(out, "Nothing to promote") {
		t.Errorf("an empty overlay produced no explanation:\n%s", out)
	}
}

// §5.1 fact 3: the `host` layer lands at the SECOND-WEAKEST precedence — under workspace,
// every config-overlay, capture, computed and managed — so a key written into the real-home
// file loses to any pack overlay that sets it. The same key promoted to `local` wins, which
// is the difference between the two destinations §5.1 says the UI must not blur.
func TestPromoteToHostLosesToEveryConfigOverlay(t *testing.T) {
	w := newPromoteWorld(t, `[]`)
	other := w.pack("other", `{"name":"other","contributes":[
	  {"kind":"config-overlay","surface":"claude/settings","config":{"managed":{"model":"theirs"}}}]}`)
	writeFile(t, filepath.Join(w.home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude",`+other+`]}`)
	w.capture("claude", "settings", `{"model":"mine"}`, `{}`)

	out, _, rc := w.run("claude", "--plan", "--to", "host")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if got := dispositionOf(t, out, "model"); got != promotionOutranked {
		t.Errorf("model --to host = %s, want %s — a config-overlay folds three slots above "+
			"the host layer:\n%s", got, promotionOutranked, out)
	}
	out, _, _ = w.run("claude", "--plan", "--to", "local")
	if got := dispositionOf(t, out, "model"); got != promotionPromotable {
		t.Errorf("model --to local = %s, want %s:\n%s", got, promotionPromotable, out)
	}
}

// `--keys` selects, and the report shows the selection rather than everything: "either way
// the confirmation lists what was selected, so `all` is never silently wider than the user
// pictured" (§5.1).
func TestPromoteKeysNarrowsTheReportToTheSelection(t *testing.T) {
	w := newPromoteWorld(t, `["claude"]`)
	w.capture("claude", "settings", `{"autoMemoryEnabled":true,"model":"mine"}`, `{}`)

	out, _, rc := w.run("claude", "--plan", "--keys", "autoMemoryEnabled")
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(out, "autoMemoryEnabled") {
		t.Errorf("the selected key is missing from the plan:\n%s", out)
	}
	if strings.Contains(out, "model") {
		t.Errorf("the plan lists a key --keys did not select:\n%s", out)
	}
}
