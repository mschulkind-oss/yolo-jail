package packload_test

import (
	"io"
	"net/http"
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
// DESIGN, so a new entry must be a conscious decision rather than drift. One entry exists
// (claude's credential dir), and the test names the consequence so a future author adding a
// second has to confirm they mean it.
//
// Ported from TestSharedDirsForIsClaudeOnlyAndSelectionGated. Its selection-gating half is
// gone with the concept: sharedDirs are now mounted for the packs actually loaded, which is
// the same gate expressed structurally.
func TestMachineGlobalTierStaysNarrow(t *testing.T) {
	got := packload.SharedDirs(loadAll(t))
	if len(got) != 1 || got[0] != ".claude-shared-credentials" {
		t.Errorf("sharedDirs across every shipped pack = %v; each entry leaks state "+
			"between workspaces by design — confirm that is intended, then update this "+
			"test", got)
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
		if got := packload.LaunchFlags(empty); len(got) != 0 {
			t.Errorf("LaunchFlags(%v) = %v, want none", empty, got)
		}
		// A command must pass through untouched rather than picking up flags from
		// nowhere.
		cmd := []string{"claude", "--print"}
		if got := packload.InjectLaunchFlags(empty, cmd); len(got) != len(cmd) {
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

	got := packload.InjectLaunchFlags(packs, []string{"copilot", "sub"})
	want := "copilot --yolo --no-auto-update sub"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
	// Alias suppression against the real declaration.
	got = packload.InjectLaunchFlags(packs, []string{"copilot", "-y", "chat"})
	if strings.Contains(strings.Join(got, " "), "--yolo") {
		t.Errorf("-y must suppress --yolo: %v", got)
	}
	// A binary no pack declares passes through.
	if got := packload.InjectLaunchFlags(packs, []string{"bash", "-c", "echo"}); len(got) != 3 {
		t.Errorf("bash must be untouched: %v", got)
	}
	// The input slice is not mutated — the caller reuses it for the attach path.
	in := []string{"copilot", "chat"}
	_ = packload.InjectLaunchFlags(packs, in)
	if strings.Join(in, " ") != "copilot chat" {
		t.Errorf("input mutated: %v", in)
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
// NETWORK-GATED, so it is not part of the offline unit run: skipped under -short (which is
// what the pre-commit hook and the hermetic nix build use). It runs in the full `just test`
// pass, where reaching the network is already normal.
func TestNativeInstallerURLsAreLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network-gated: installer URL liveness needs the internet")
	}
	checked := 0
	for _, p := range loadAll(t) {
		inst := p.Decl.InstallContribution()
		if inst == nil || inst.InstallerURL == "" {
			continue
		}
		url := inst.InstallerURL
		t.Run(p.Name, func(t *testing.T) {
			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Get(url)
			if err != nil {
				// A network hiccup must not fail the suite — the assertion is about the URL
				// being WRONG, not about this machine's connectivity.
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
				t.Errorf("%s installerUrl %s serves a WEB PAGE, not a script — piping this "+
					"into bash is the \"syntax error near unexpected token `<'\" failure. "+
					"Find the tool's current install command and update the pack.",
					p.Name, url)
			}
		})
		checked++
	}
	if checked == 0 {
		t.Error("no native installer URLs found — this test has silently stopped covering anything")
	}
}
