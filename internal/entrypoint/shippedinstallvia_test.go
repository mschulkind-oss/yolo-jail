package entrypoint

// shippedinstallvia_test.go pins WHICH DELIVERY MECHANISM EACH SHIPPED AGENT CLI ARRIVES BY,
// and pins it through the consumer rather than through the manifest bytes.
//
// The distinction is the whole reason this file exists. A test that re-read
// packs/codex/pack.json and asserted `"via": "installer"` would be a test of a string
// against itself: it goes green whether or not anything in the jail honours the value, so
// deleting GenerateAgentLaunchers' `case "native":` arm would leave it passing. What is
// asserted here is the LAUNCHER the boot path actually writes into ~/.yolo/bin/launch —
// boot.go's own call — so the cell goes red for either mutation: flipping a manifest back,
// or teaching the generator to ignore the field.
//
// Why it is worth pinning at all (docs/design/program-delivery.md §3.5, OQ-PD13): an
// npm-installed agent CLI structurally cannot self-update — copilot's updater refuses with
// "Update not supported when running js directly" behind a `node:sea.isSea()` gate,
// measured in @github/copilot 1.0.48's app.js — while the vendors' own installers both
// self-update and accept a version. So `via` is not a packaging detail; it decides whether
// a jail's agent can ever move. A silent revert is a freeze nobody would notice.
//
// THE TABLE IS DELIBERATELY EXHAUSTIVE. Every program contribution any shipped pack makes
// must appear below, and the set is compared both ways: a new agent pack, or a seventh
// program on an existing one, fails here until someone states which mechanism delivers it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// shippedDelivery is the declared delivery of every program the official packs contribute,
// keyed by the bin name the launcher is filed under.
//
// `source` is the mechanism's one load-bearing input — the installer URL, or the npm
// package selector — and it is spelled out rather than derived so that a URL typo is a
// failing test rather than a jail that curls the wrong host. Each was re-fetched and read
// on 2026-09-04: gh.io/copilot-install and chatgpt.com/codex/install.sh both answer 200
// with a shell script.
//
// `update` is the pack's declared update verb (OQ-PD14), spelled here as the whole argv the
// program is run with. EMPTY means the pack declares none and the launcher falls back per
// `via` — `npm install -g <package>`, or a re-run of the installer — which is a real answer
// and not a gap: for the three npm-delivered agents the registry IS the vendor's channel,
// so a second, unmeasured path to it would buy nothing.
var shippedDelivery = map[string]struct {
	pack      string
	mechanism string // "installer" or "npm"
	source    string
	update    string // the declared verb's argv, or "" for the via fallback
}{
	"claude": {"claude", "installer", "https://claude.ai/install.sh", "install"},
	"agy":    {"agy", "installer", "https://antigravity.google/cli/install.sh", "update"},
	// FLIPPED FROM npm 2026-09-04 (OQ-PD13). codex's installer takes
	// `${CODEX_INSTALL_DIR:-$HOME/.local/bin}` with no root branch, so its default landing
	// path is exactly nativeLauncherTemplate's REAL_BIN — which is the constraint the flip
	// turns on, and the one copilot fails (see the comment on its row).
	"codex": {"codex", "installer", "https://chatgpt.com/codex/install.sh", "update"},
	// NOT FLIPPED, and not for want of an installer. gh.io/copilot-install picks
	// `PREFIX=/usr/local` when `id -u` is 0 and `$HOME/.local` otherwise; a container-backend
	// jail runs as root under an unconditional `--read-only` rootfs (assemble.go), so its
	// `mkdir -p /usr/local/bin` fails and the installer exits 1 having landed nothing.
	// Measured in this jail 2026-09-04. The manifest cannot pass `PREFIX=`, so the flip has
	// to wait for a way to say it.
	"copilot":  {"copilot", "npm", "@github/copilot", ""},
	"opencode": {"opencode", "npm", "opencode-ai", ""},
	// pi's "native installer" IS npm — pi.dev/install.sh runs `npm install -g
	// @earendil-works/pi-coding-agent` into npm's global prefix — so a flip would change
	// nothing about delivery and would break the launcher's REAL_BIN.
	//
	// NO DECLARED VERB, which corrects the note that stood here. §3.5's table gives pi's verb
	// as `pi update --self` and says pi "is evergreen ONLY through" it — that was written on
	// the assumption pi would flip to a native installer, and it did not. Under npm delivery
	// the launcher's own `npm install -g <pkg>` resolves the registry's latest, which is the
	// same channel `pi update --self` would reach and the one measured to work here. Running
	// the verb on top would be an unmeasured second path to one answer.
	"pi": {"pi", "npm", "@earendil-works/pi-coding-agent", ""},
}

// stageShippedPacks materializes the embedded official packs where the boot path expects
// them — <YOLO_PACK_ROOT>/_official/<name>, the shape stagePacks produces — and returns the
// root.
func stageShippedPacks(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "packs")
	official := filepath.Join(root, "_official")
	if err := os.MkdirAll(official, 0o755); err != nil {
		t.Fatal(err)
	}
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, official)
	if len(problems) > 0 {
		t.Fatalf("materializing the shipped packs: %v", problems)
	}
	if len(packs) == 0 {
		t.Fatal("no embedded packs materialized")
	}
	return root
}

// TestShippedAgentLaunchersUseTheDeclaredMechanism is the call-site cell.
func TestShippedAgentLaunchersUseTheDeclaredMechanism(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": stageShippedPacks(t)})

	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatalf("GenerateAgentLaunchers over the shipped packs: %v", err)
	}

	written, err := os.ReadDir(e.LaunchDir())
	if err != nil {
		t.Fatalf("reading the launcher dir: %v", err)
	}
	got := map[string]bool{}
	for _, ent := range written {
		got[ent.Name()] = true
	}

	for bin, want := range shippedDelivery {
		if !got[bin] {
			t.Errorf("no launcher written for %q — pack %s declares a program and the boot "+
				"path delivered nothing", bin, want.pack)
			continue
		}
		body, err := os.ReadFile(filepath.Join(e.LaunchDir(), bin))
		if err != nil {
			t.Fatalf("reading the %s launcher: %v", bin, err)
		}
		checkLauncherMechanism(t, bin, want.mechanism, want.source, string(body))
		checkLauncherUpdateVerb(t, bin, want.update, string(body))
	}
	for bin := range got {
		if _, expected := shippedDelivery[bin]; !expected {
			t.Errorf("a shipped pack now contributes program %q and this table does not say "+
				"how it is delivered — add a row saying installer or npm, and why", bin)
		}
	}
}

// checkLauncherMechanism asserts the generated body is the template for `mechanism` and
// carries `source`, and — the half that catches a half-applied flip — that it carries no
// trace of the other template. A launcher that curled the vendor while still npm-installing
// would satisfy a one-sided check.
func checkLauncherMechanism(t *testing.T, bin, mechanism, source, body string) {
	t.Helper()
	// The two templates' REAL_BIN lines are the cleanest discriminator: they are the whole
	// reason `via` matters at run time, since REAL_BIN is where the launcher then looks.
	const (
		nativeRealBin = `REAL_BIN="$HOME/.local/bin/$BIN"`
		npmRealBin    = `REAL_BIN="$NPM_CONFIG_PREFIX/bin/$BIN"`
	)
	wantMarker, unwantMarker := nativeRealBin, npmRealBin
	if mechanism == "npm" {
		wantMarker, unwantMarker = npmRealBin, nativeRealBin
	}
	if !strings.Contains(body, wantMarker) {
		t.Errorf("%s launcher is not the %s template — expected %s", bin, mechanism, wantMarker)
	}
	if strings.Contains(body, unwantMarker) {
		t.Errorf("%s launcher carries the OTHER template's %s", bin, unwantMarker)
	}
	// The source, and the assignment that carries it. Two separate assertions rather than
	// one composed literal, on purpose: the emitted line is `URL=<shquote'd source>`, so
	// composing the expectation with shquote.Quote would re-derive the generator's own
	// quoting and go green on a mutated quoter. The pair is independent of it — a typo'd
	// URL fails the first, a value assigned to the wrong variable fails the second — and
	// the splice contract itself is pinned by launchersplice_test.go.
	assignVar := "URL"
	if mechanism == "npm" {
		assignVar = "PKG"
	}
	if !strings.Contains(body, source) {
		t.Errorf("%s launcher does not carry %q — the pack's declared source did not reach "+
			"the launcher", bin, source)
	}
	if !strings.Contains(body, "\n"+assignVar+"=") {
		t.Errorf("%s launcher has no %s= assignment — it is not the %s template it claims "+
			"to be", bin, assignVar, mechanism)
	}
	// And no residue of the mechanism it is NOT: an installer-delivered agent must never
	// reach the registry, and an npm one must never curl a script into bash.
	if mechanism == "installer" && strings.Contains(body, "npm install -g") {
		t.Errorf("%s is delivered by its vendor's installer but its launcher still npm "+
			"installs — an npm-installed CLI cannot self-update (OQ-PD13)", bin)
	}
	if mechanism == "npm" && strings.Contains(body, "curl -fsSL \"$URL\"") {
		t.Errorf("%s is delivered by npm but its launcher curls an installer", bin)
	}
}

// checkLauncherUpdateVerb is the call-site cell for OQ-PD14's per-pack values: a verb in a
// pack.json that never reaches the emitted launcher is a declaration that silently does
// nothing, and the whole point of the field is that core stops guessing.
//
// It asserts the FLAG as well as the words, because they are two different mutations: drop
// the projection and the flag goes to 0 with the array empty; drop the flag and the launcher
// runs the verb branch against an empty argv (which for `claude` means starting the agent).
func checkLauncherUpdateVerb(t *testing.T, bin, verb, body string) {
	t.Helper()
	if verb == "" {
		// No verb declared: the launcher must not carry one either. Asserted as the
		// ABSENCE of the enabled flag rather than the presence of a disabled one, so the
		// cell reads the same for a template with no verb machinery at all (npm's, whose
		// fallback IS `npm install -g`) and for one that has it switched off.
		if strings.Contains(body, "HAS_UPDATE_VERB=1") {
			t.Errorf("%s declares no update verb, but its launcher carries one", bin)
		}
		return
	}
	if !strings.Contains(body, "\nHAS_UPDATE_VERB=1\n") {
		t.Errorf("%s declares update verb %q, but its launcher does not enable the verb "+
			"branch — the declaration reached nothing", bin, verb)
	}
	// The flag and the words are two different mutations: drop the projection and the flag
	// goes to 0 with the array empty; drop the flag and the launcher runs the verb branch
	// against an empty argv, which for `claude` means starting the agent.
	if want := "UPDATE_VERB=(" + verb + ")"; !strings.Contains(body, want) {
		t.Errorf("%s launcher should carry %q — the pack's declared verb did not reach it",
			bin, want)
	}
}
