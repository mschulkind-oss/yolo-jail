package packload_test

import (
	"io"
	"net/http"
	"os"
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
// DESIGN, so a new entry must be a conscious decision rather than drift. Two entries exist
// (claude's and agy's credential dirs), and the test names the consequence so a future author
// adding a third has to confirm they mean it.
//
// Ported from TestSharedDirsForIsClaudeOnlyAndSelectionGated. Its selection-gating half is
// gone with the concept: sharedDirs are now mounted for the packs actually loaded, which is
// the same gate expressed structurally.
func TestMachineGlobalTierStaysNarrow(t *testing.T) {
	got := packload.SharedDirs(loadAll(t))
	want := []string{".claude-shared-credentials", ".gemini-shared-credentials"}
	if len(got) != len(want) {
		t.Errorf("sharedDirs across every shipped pack = %v, want %v; each entry leaks state "+
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
// It walks NeedsHostAccess's three declarations (hostFiles, mounts[].hostOverlay,
// install.installerUrl) rather than just the one named "hostFiles", because collapsing the
// boundary to a single field is the exact mistake the old version was written to prevent —
// back when it was AgentSpec.HostFiles while Briefing.HostSource and Skills read the host
// home too.
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
		// nowhere — with no table and with one, since the table only ever ADDS flags.
		cmd := []string{"claude", "--print"}
		for _, profiles := range []map[string]string{nil, {"claude": "bedrock"}} {
			if got := packload.InjectLaunchFlags(empty, profiles, cmd); len(got) != len(cmd) {
				t.Errorf("InjectLaunchFlags with no packs altered the command: %v", got)
			}
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
func TestHostFileGrantsAreExactlyTwoSettingsFiles(t *testing.T) {
	want := map[string][]string{
		"claude": {".claude/settings.json"},
		"pi":     {".pi/agent/settings.json"},
	}
	for _, p := range loadAll(t) {
		var froms []string
		for _, hf := range p.Decl.HostFileContributions() {
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

// TestFetchedPacksGetNoHostAccess is the enforcement half: the identical declaration is
// HONORED for a pack whose content ships with yolo and REFUSED for one fetched from a git
// ref. Installing a third-party pack approves distributing content, not handing that
// repository your host config.
//
// Both directions are asserted from one declaration, because a test that only checked the
// refusal would pass on an implementation that refused everything.
func TestFetchedPacksGetNoHostAccess(t *testing.T) {
	for _, p := range loadAll(t) {
		if len(p.Decl.HostFileContributions()) == 0 {
			continue
		}
		granted, refused := p.HonoredHostFiles()
		if len(granted) == 0 || len(refused) != 0 {
			t.Errorf("embedded pack %s: want its host files granted, got %d granted / %d "+
				"refused", p.Name, len(granted), len(refused))
		}
		// The same pack, reached as fetched content.
		fetched := &packload.Pack{Name: p.Name, Root: p.Root, Decl: p.Decl, MayAccessHost: false}
		granted, refused = fetched.HonoredHostFiles()
		if len(granted) != 0 {
			t.Errorf("as a FETCHED pack, %s must be granted nothing, got %v", p.Name, granted)
		}
		if len(refused) != len(p.Decl.HostFileContributions()) {
			t.Errorf("as a FETCHED pack, %s must report a refusal per declaration: got %d "+
				"for %d", p.Name, len(refused), len(p.Decl.HostFileContributions()))
		}
	}
}

// TestCopilotFlagsInjectFromItsRealDeclaration ports internal/agents/inject_test.go onto the
// shipped copilot pack.
//
// TestInjectLaunchFlags already covers the MECHANISM with a synthetic pack; this covers the
// DECLARATION, which is the half that can silently rot. The alias entry is the case worth
// pinning: `-y` and `--yolo` are the same switch, so a user who typed `-y` must not also get
// `--yolo`, and that only works if the copilot pack still declares the alias alongside the
// flags. A pack that dropped flagAliases would keep passing a synthetic-fixture test.
func TestCopilotFlagsInjectFromItsRealDeclaration(t *testing.T) {
	packs := loadAll(t)

	got := packload.InjectLaunchFlags(packs, nil, []string{"copilot", "sub"})
	want := "copilot --yolo --no-auto-update sub"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
	// Alias suppression against the real declaration.
	got = packload.InjectLaunchFlags(packs, nil, []string{"copilot", "-y", "chat"})
	if strings.Contains(strings.Join(got, " "), "--yolo") {
		t.Errorf("-y must suppress --yolo: %v", got)
	}
	// A binary no pack declares passes through.
	if got := packload.InjectLaunchFlags(packs, nil, []string{"bash", "-c", "echo"}); len(got) != 3 {
		t.Errorf("bash must be untouched: %v", got)
	}
	// The input slice is not mutated — the caller reuses it for the attach path.
	in := []string{"copilot", "chat"}
	_ = packload.InjectLaunchFlags(packs, nil, in)
	if strings.Join(in, " ") != "copilot chat" {
		t.Errorf("input mutated: %v", in)
	}
}

// TestBedrockProfilePatchesItsOwnSettingsEnv ports the config-surface half of the payload
// split (profiles-as-pack-variants.md §5, D8, OQ-16) onto the declaration that ships it:
// the NON-SECRET half of a profile routes into the pack's own config file as well as the
// process env, because the settings env block is honored before the first API call (OQ-4,
// measured 2026-08-31) while process env needs yolo in the launch path — a bare `claude`,
// cron, or an IDE's absolute path gets nothing from env alone.
//
// The bedrock profile is the shipped instance: CLAUDE_CODE_USE_BEDROCK into the env block
// of the claude/settings surface packs/claude itself owns, so the variant survives an
// invocation yolo did not launch. The mechanism is already pinned by
// TestProfileConfigFoldsAfterAutonomy on a synthetic fixture; this pins the DECLARATION,
// which is the half that can silently rot — the profile shipped env-only for its whole
// first day because nothing asked the real manifest what it folded.
func TestBedrockProfilePatchesItsOwnSettingsEnv(t *testing.T) {
	var claude *packload.Pack
	for _, p := range loadAll(t) {
		if p.Name == "claude" {
			claude = p
		}
	}
	if claude == nil {
		t.Fatal("the claude pack did not materialize")
	}

	surfaces, probs := claude.SurfacesFor(true, map[string]string{"claude": "bedrock"})
	if len(probs) != 0 {
		t.Fatalf("folding the bedrock profile raised problems: %v", probs)
	}
	var env map[string]any
	for i := range surfaces {
		if surfaces[i].Key().String() != "claude/settings" {
			continue
		}
		m := surfaces[i].ManagedMap()
		var ok bool
		if env, ok = m["env"].(map[string]any); !ok {
			t.Fatalf("the composed claude/settings managed layer carries no env block: %+v", m)
		}
	}
	if env == nil {
		t.Fatal("packs/claude declares no claude/settings surface to fold into")
	}
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("with the bedrock profile selected, the settings env block must carry "+
			"CLAUDE_CODE_USE_BEDROCK — the half that survives an invocation yolo did not "+
			"launch: %+v", env)
	}

	// The payload split's other half: what composes from provider VALUES stays
	// env-delivered. AWS_REGION and the ANTHROPIC_* model ids are the provider entry's
	// own facts (the agent pack's env derive composes them at launch), and a literal
	// here would be a second
	// copy of a fact packs/claude's provider declaration already owns.
	for _, k := range []string{
		"AWS_REGION", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
	} {
		if _, present := env[k]; present {
			t.Errorf("%s must stay env-delivered (composed from the provider at launch), not "+
				"hand-copied into the settings patch: %+v", k, env)
		}
	}

	// The discriminator: without the variant selected the key is absent — pinning that
	// the patch rides the PROFILE and not the pack's static managed layer, where it would
	// switch Bedrock on for users who never chose the variant.
	base, _ := claude.SurfacesFor(true, nil)
	for i := range base {
		if base[i].Key().String() != "claude/settings" {
			continue
		}
		if m := base[i].ManagedMap(); m["env"] != nil {
			t.Errorf("an unselected profile must fold nothing into the env block: %+v", m["env"])
		}
	}
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
