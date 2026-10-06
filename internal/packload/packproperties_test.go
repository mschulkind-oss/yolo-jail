package packload_test

import (
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// This file ports the three properties that internal/agents/agents_test.go was really
// guarding, from the deleted AgentSpec registry onto the pack declarations that replaced
// it. Each one is about a boundary, not about which tools exist, which is why they outlived
// the registry.

func loadAll(t *testing.T) []*packload.Pack {
	t.Helper()
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	if len(packs) == 0 {
		t.Fatal("no embedded packs")
	}
	return packs
}

// TestMachineGlobalTierStaysNarrow: anything in sharedDirs leaks between workspaces BY
// DESIGN, so a new entry must be a conscious decision rather than drift. The test names the
// consequence so a future author adding one has to confirm they mean it.
//
// ⚠ A THIRD ENTRY WAS HERE AND LEFT. `.pi-shared-npm`, pi's extension package store, was
// added deliberately on 2026-09-21 (docs/design/pi-extension-lifecycle.md OQ-1) so every
// workspace ran one version of an extension, and it went on 2026-10-05 (XB-D14 of
// docs/design/pi-extension-store-builds.md): one jail's refresh rewrote the npm prefix every
// pi jail loaded from, against the no-leakage ruling. pi now unshares the link a home kept to
// it, and the list is credential dirs alone again.
//
// Ported from TestSharedDirsForIsClaudeOnlyAndSelectionGated. Its selection-gating half is
// gone with the concept: sharedDirs are now mounted for the packs actually loaded, which is
// the same gate expressed structurally.
func TestMachineGlobalTierStaysNarrow(t *testing.T) {
	got := packload.SharedDirs(loadAll(t))
	want := []string{".claude-shared-credentials", ".gemini-shared-credentials"}
	if len(got) != len(want) {
		t.Fatalf("sharedDirs across every shipped pack = %v, want %v; each entry leaks state "+
			"between workspaces by design — confirm that is intended, then update this "+
			"test", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sharedDirs[%d] = %q, want %q (full set %v)", i, got[i], want[i], got)
		}
	}
}

// TestEveryHostHomeReadIsHomeRelative is the CREDENTIAL BOUNDARY shape check.
//
// The claim it pins: every declaration that reads the host home must be a plain
// $HOME-relative path. An absolute path or a `..` escape would read outside the user's home
// entirely — a fetched pack naming "../../etc/shadow" is the case that makes this a security
// property rather than tidiness.
//
// It walks EVERY declaration that reads the host home (host files, mounts[].hostOverlay,
// hooks' file and sharedDir) rather than just the one named "hostFiles", because collapsing
// the boundary to a single field is the exact mistake the old version was written to
// prevent — back when it was AgentSpec.HostFiles while Briefing.HostSource and Skills read
// the host home too.
func TestEveryHostHomeReadIsHomeRelative(t *testing.T) {
	for _, p := range loadAll(t) {
		for _, hf := range p.Decl.HostFileContributions() {
			checkHomeRelative(t, p.Name, "hostFiles.from", hf.From)
		}
		for _, mt := range p.Decl.MountContributions() {
			checkHomeRelative(t, p.Name, "mounts[].hostOverlay", mt.HostOverlay)
		}
		for _, h := range p.Decl.HookContributions() {
			checkHomeRelative(t, p.Name, "hooks[].file", h.File)
			checkHomeRelative(t, p.Name, "hooks[].sharedDir", h.SharedDir)
		}
	}
}

func checkHomeRelative(t *testing.T, pack, field, p string) {
	t.Helper()
	if p == "" {
		return
	}
	if strings.HasPrefix(p, "/") {
		t.Errorf("%s %s = %q: must be $HOME-relative", pack, field, p)
	}
	if strings.Contains(p, "..") {
		t.Errorf("%s %s = %q: must not escape the home dir", pack, field, p)
	}
}

// TestNoPacksMeansNoDeclarations: every union must tolerate an empty pack set without
// panicking and without inventing anything.
//
// This is the descendant of TestEmptyAgentSelectionIsSupported, and the reason it survived
// the registry is that the failure it guards is still available: a fallback here would give
// a jail mounts and dirs for a tool its config never asked for, silently. A user with no
// packs configured is a supported state — they get a warning at launch, not a fabricated
// selection.
func TestNoPacksMeansNoDeclarations(t *testing.T) {
	for _, empty := range [][]*packload.Pack{nil, {}} {
		if got := packload.WritableDirs(empty); len(got) != 0 {
			t.Errorf("WritableDirs(%v) = %v, want none", empty, got)
		}
		if got := packload.SharedDirs(empty); len(got) != 0 {
			t.Errorf("SharedDirs(%v) = %v, want none", empty, got)
		}
		// RetireMiseTools is deliberately NOT per-pack any more (OQ11): it returns a
		// fixed CORE list of yolo's own retired mise tokens regardless of packs, so it
		// is not asserted empty here.
		//
		// A command must pass through untouched rather than picking up flags from
		// nowhere — the injection reads the packs and nothing else, since the profile
		// table it used to also fold died with the kind's body (OQ-PT8).
		cmd := []string{"claude", "--print"}
		if got, _ := packload.InjectLaunchFlags(empty, true, cmd); len(got) != len(cmd) {
			t.Errorf("InjectLaunchFlags with no packs altered the command: %v", got)
		}
	}
}

// TestHostFileGrantsAreExactlyTwoSettingsFiles pins the CONTENTS of the credential
// boundary, not just its shape: which host files cross into a jail, and from which pack.
//
// Ported from internal/agents/hostfiles_test.go. Two packs read a host file and each reads
// exactly settings.json; every other shipped pack crosses nothing. The list is short on
// purpose, and a test that merely accepted whatever the packs declared would let it grow
// silently — which is precisely what the retired host_claude_files/host_pi_files config keys
// allowed and why they were removed.
//
// THROUGH HonoredHostFiles, NOT Decl.HostFileContributions, since OQ-CO10 moved the
// config-surface half of the declaration onto the surface (2026-09-12). The boundary is
// what CROSSES, and there are now two declarations that can widen it — a `reads-host`
// contribution and a surface's `readsHost` — so a test that walked only the first would
// have gone green on the day both shipped packs stopped using it, while reporting that
// nothing crosses at all.
func TestHostFileGrantsAreExactlyTwoSettingsFiles(t *testing.T) {
	want := map[string][]string{
		"claude": {".claude/settings.json"},
		"pi":     {".pi/agent/settings.json"},
	}
	for _, p := range loadAll(t) {
		var froms []string
		granted, _ := p.HonoredHostFiles()
		for _, hf := range granted {
			froms = append(froms, hf.From)
		}
		expected, listed := want[p.Name]
		if !listed {
			if len(froms) != 0 {
				t.Errorf("pack %s crosses host files %v — the credential boundary must not "+
					"grow silently; if this is intended, add it to this test", p.Name, froms)
			}
			continue
		}
		if len(froms) != len(expected) {
			t.Errorf("pack %s hostFiles = %v, want %v", p.Name, froms, expected)
			continue
		}
		for i := range expected {
			if froms[i] != expected[i] {
				t.Errorf("pack %s hostFiles[%d] = %q, want %q", p.Name, i, froms[i], expected[i])
			}
		}
	}
}

// TestEveryShippedPacksHostFilesAreHonored is what is left of TestFetchedPacksGetNoHostAccess,
// which asserted that the identical declaration was HONORED for an embedded pack and REFUSED
// for one reached as fetched content. OQ-TP9 (docs/design/trust-paths.md, 2026-09-04) deleted
// that split — origin no longer decides anything — so what remains is the half that was never
// about origin: a shipped pack's declared host files must actually be delivered.
//
// It is not tautological beside the shape check above. That one walks the DECLARATIONS; this
// one walks what HonoredHostFiles returns, so a gate reintroduced in packload (rather than at
// a call site, which packnohostgate_test.go covers) turns this red.
func TestEveryShippedPacksHostFilesAreHonored(t *testing.T) {
	for _, p := range loadAll(t) {
		want := len(p.Decl.HostFileContributions()) + len(p.SurfaceHostFiles())
		if want == 0 {
			continue
		}
		granted, refused := p.HonoredHostFiles()
		if len(granted) != want {
			t.Errorf("pack %s declares %d host file(s) and %d were honored", p.Name, want,
				len(granted))
		}
		if len(refused) != 0 {
			t.Errorf("pack %s had host files REFUSED: %v\nThe origin gate is deleted; a "+
				"refusal here is a gate that came back without a ruling", p.Name, refused)
		}
	}
}

// TestCopilotFlagsInjectFromItsRealDeclaration ports internal/agents/inject_test.go onto the
// shipped copilot pack.
//
// TestInjectLaunchFlags already covers the MECHANISM with a synthetic pack; this covers the
// DECLARATION, which is the half that can silently rot — `--yolo` is declared under
// `autonomy`, three kinds and one notch policy away from the injector that delivers it, so
// nothing about a synthetic fixture says the shipped pack still reaches it.
//
// THE `-y` CASE IS GONE ON PURPOSE, and its absence is pinned next door by
// TestTypingTheShortSpellingNoLongerSuppressesTheInjectedFlag: the alias map that made `-y`
// suppress `--yolo` was deleted, so this file must not quietly keep asserting the behaviour
// under a different name.
//
// The exact-match `want` is also what pins `--no-auto-update` GONE: the flag is dropped, so a
// jail's copilot polls for its own updates again (docs/plans/native-installer-migration.md,
// the Blockers bullet's option A).
func TestCopilotFlagsInjectFromItsRealDeclaration(t *testing.T) {
	packs := loadAll(t)

	got, _ := packload.InjectLaunchFlags(packs, true, []string{"copilot", "sub"})
	want := "copilot --yolo sub"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
	// A binary no pack declares passes through.
	if got, _ := packload.InjectLaunchFlags(packs, true, []string{"bash", "-c", "echo"}); len(got) != 3 {
		t.Errorf("bash must be untouched: %v", got)
	}
	// The input slice is not mutated — the caller reuses it for the attach path.
	in := []string{"copilot", "chat"}
	_, _ = packload.InjectLaunchFlags(packs, true, in)
	if strings.Join(in, " ") != "copilot chat" {
		t.Errorf("input mutated: %v", in)
	}
}

// TestNoShippedPackKeysAFactOnAProfileName pins OQ-BR8 (docs/design/providers-and-profiles-
// redesign.md, ruled 2026-09-29) on the manifests yolo ships: a provider fact keys on the
// PROVIDER, in the agent's own derive or on a `platform` gate, never on a profile's NAME. The
// `profile` modifier stays for a pack whose variant really is a name, but every shipped use of
// it was a provider fact a second profile over the same provider lost (trap D5): claude's
// CLAUDE_CODE_USE_BEDROCK in env and in claude/settings, its codex login prelaunch, pi's codex
// prelaunch, llamacpp's attribution header and aws-auth's credentials pointer. A shipped
// contribution that reintroduces one fails here, naming it.
func TestNoShippedPackKeysAFactOnAProfileName(t *testing.T) {
	for _, p := range loadAll(t) {
		for _, c := range p.Decl.GatedEnvContributions() {
			if c.Profile != "" {
				t.Errorf("pack %s ships an env contribution gated on the profile name %q (%v): key "+
					"it on the provider — its agent's derive, or a `platform` gate", p.Name, c.Profile,
					sortedKeysOf(c.Vars))
			}
		}
		for _, ov := range p.Decl.ConfigOverlayContributions() {
			if ov.Profile != "" {
				t.Errorf("pack %s ships a config-overlay on %s gated on the profile name %q: key it "+
					"on the provider in the surface owner's derive", p.Name, ov.Surface, ov.Profile)
			}
		}
	}
}

func sortedKeysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestNativeInstallerURLsAreLive fetches every shipped installerUrl and asserts it still
// serves a shell script.
//
// This exists because the failure mode is INVISIBLE to every other test. An installer
// endpoint that moves usually keeps answering 200 with a web page, so the URL looks fine
// from Go: the string is well-formed, the pack validates, the launcher generates. The break
// only appears when a user runs the tool and bash chokes on HTML. agy shipped with
// antigravity.google.com/install.sh — a placeholder that was never replaced, and which
// answered 200/text/html — for five days before anyone ran it.
//
// NETWORK-GATED, and gated on its OWN knob rather than on -short. -short means exactly one
// thing in this repo — "do not start containers" — and it is what integration/'s requireJail
// reads. This test starts no container; it egresses to two third-party hosts. Hanging that
// on -short made it invisible, because every invocation in the tree passes -short
// (Justfile `test` and `test-fast`, ci.yml's check-go and check-macos) and the only
// non-short target is ./integration, which does not contain this test. So it ran in zero
// recipes and zero CI jobs — a rot-detector that had itself rotted.
//
// Un-gating outright is the wrong correction: a non-200 here is a hard failure, not a skip
// (only a transport error skips, below), so someone else's CDN — claude.ai sits behind
// Cloudflare — could redden a PR that changed nothing. Opt in with YOLO_TEST_NETWORK=1.
//
// NOTE: nothing in-tree sets that variable yet, so this test still executes nowhere. That is
// now a visible, one-line-to-close gap (a scheduled job that exports it) instead of a gate
// disguised as a speed optimization.
func TestNativeInstallerURLsAreLive(t *testing.T) {
	if os.Getenv("YOLO_TEST_NETWORK") != "1" {
		t.Skip("needs outbound HTTPS to third-party installer hosts; set YOLO_TEST_NETWORK=1 to run")
	}
	checked := 0
	for _, p := range loadAll(t) {
		// EVERY program contribution: a pack declaring two installers must have both URLs
		// checked, not just the first one the manifest happens to list.
		for _, inst := range p.Decl.InstallContributions() {
			if inst.InstallerURL == "" {
				continue
			}
			url := inst.InstallerURL
			t.Run(p.Name+"/"+inst.Bin, func(t *testing.T) {
				client := &http.Client{Timeout: 30 * time.Second}
				resp, err := client.Get(url)
				if err != nil {
					// A network hiccup must not fail the suite — the assertion is about the
					// URL being WRONG, not about this machine's connectivity.
					t.Skipf("cannot reach %s: %v", url, err)
				}
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != http.StatusOK {
					t.Errorf("%s installerUrl %s returned %d — the endpoint moved",
						p.Name, url, resp.StatusCode)
					return
				}
				head := make([]byte, 512)
				n, _ := io.ReadAtLeast(resp.Body, head, 1)
				body := strings.ToLower(strings.TrimSpace(string(head[:n])))
				if strings.HasPrefix(body, "<!doctype") || strings.HasPrefix(body, "<html") {
					t.Errorf("%s installerUrl %s serves a WEB PAGE, not a script — piping "+
						"this into bash is the \"syntax error near unexpected token `<'\" "+
						"failure. Find the tool's current install command and update the pack.",
						p.Name, url)
				}
			})
			checked++
		}
	}
	if checked == 0 {
		t.Error("no native installer URLs found — this test has silently stopped covering anything")
	}
}
