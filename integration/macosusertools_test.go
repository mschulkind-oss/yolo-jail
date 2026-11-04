package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// RUNBOOK ITEM 9 — `mise_tools` actually arrive
// (docs/plans/runbooks/macos-user-manual-checks.md).
//
// ⚠ ITS `lsp_servers` HALF IS GONE, and not because it failed. It asked whether the server
// the recipe table installed (`python` → pyright) reached the sandbox's npm prefix. That
// table and every install path it fed are deleted (docs/reference/mcp-configuration.md#oq-lsp1):
// yolo installs no language server on ANY backend now, and a configured server's `command`
// must already resolve on PATH. There is no install left to arrive, so the subtest went with
// it; internal/macosuser/lspservers_test.go pins, from Linux, that this backend asks for none.
//
// WHAT IT SETTLES. Two launch warnings were RETIRED on 2026-09-12 — `mise_tools` and
// `lsp_servers` each used to say "this backend installs nothing" — and both were removed
// on the strength of code that had never run
// (docs/reference/macos-user-provisioning.md, "The two retired warnings, and the rule that retired them", whose own ⚠ says *"the absence of a
// warning is not evidence of a feature"*). This is the evidence. If a declared tool is
// absent here, that retirement was premature and the warning it deleted should come back.
//
// THE TIER CHECK IS THE HALF THAT CANNOT BE UNDONE BY A LATER LAUNCH, which is why it is
// asserted beside the installs rather than left to item 5. The mise store is the MACHINE
// tier: one tool store for every workspace, the way podman mounts one /mise
// (macosuser.SandboxMiseData states the rule), while `~/.yolo/bin` and `~/.npm-global` are
// the WORKSPACE tier. Getting that backwards gives every workspace its own copy of every
// tool — the inverse of podman, with none of the reason Apple Container has for one disk per
// workspace (a disk attaches to one VM at a time, OQ-MB1) — and nothing at run time
// complains.
//
// THE GEOMETRY SINCE SLICE 12a (MMP-D25 in docs/design/macos-user-model-plan.md). Account mode
// runs with HOME = <ws>/.yolo/home, the workspace home itself, so `~/.yolo/bin` and
// `~/.npm-global` are REAL directories in it (entrypoint.DeriveDarwinWorkspaceHome), no longer
// links from the account home into the sidecar. The mise store did NOT move with HOME: it is
// MISE_DATA_DIR = macosuser.SandboxMiseData(<machine root>), and account mode's machine root is
// /Users/_yolojail, so the store is still /Users/_yolojail/.yolo/mise — now OUTSIDE HOME, and
// reached only through MISE_DATA_DIR (no link in HOME carries it). That is why the probe below
// reads the store at "$MISE_DATA_DIR" and never at "$HOME/.yolo/mise", which in a 12a
// session is a path nothing creates.
//
// IT RUNS ONE LAUNCH AND SPLITS THE ANSWER INTO SUBTESTS, on this suite's fencing
// convention: a macos-user launch builds a native nix closure and then installs from the
// network, so asking three questions in three launches would cost three of those. A subtest
// inherits the TestMacosUser… prefix the gate requires, so the CI filter still selects it.
func TestMacosUserDeclaredToolsArrive(t *testing.T) {
	requireMacosUser(t)

	// The declared tool is the runbook's own (`mise_tools: {jq: latest}`). `latest` rather
	// than a pin because a pinned version can be yanked and this test would then fail for
	// a reason that is nobody's bug; the assertion is on the tool's NAME, which a version
	// bump cannot move.
	// ⚠ The floor ALSO ships a nix `jq`, so `command -v jq` in this sandbox will show a
	// store path even when mise installed nothing. That is why nothing below asks PATH:
	// item 9 is about ARRIVAL, and `mise ls` plus the store directory are what answer it.
	const miseTool = "jq"
	wsConfig := fmt.Sprintf(`{"mise_tools": {%q: "latest"}}`, miseTool)

	ws := macosUserWorkspace(t, wsConfig)
	// The startup log is the stage's own record, and the ONE place that says why an
	// install is missing. Attached on failure rather than always, so a green run stays
	// readable — and read from a cleanup, which runs before the workspace is removed
	// (cleanups are LIFO and macosUserWorkspace registered its removal first).
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		t.Logf("provisioning log for the failure(s) above — %s:\n%s",
			provision.StartupLog(ws), readProvisionLog(ws))
	})

	r := runMacosUser(t, ws, macosUserToolsProbe())
	if r.rc != 0 {
		t.Fatalf("the macos-user launch failed (rc %d) before any probe could answer "+
			"(runbook item 9). A non-zero rc here is the PROVISIONING VETO or a launch "+
			"fault, not a missing tool — read the log below.\nstdout:\n%s\nstderr:\n%s\n"+
			"%s:\n%s", r.rc, r.stdout, r.stderr, provision.StartupLog(ws), readProvisionLog(ws))
	}
	if strings.TrimSpace(section(r.stdout, "=== TIERS ===", "=== END ===")) == "" {
		t.Fatalf("the sandbox produced no probe output, so nothing below can be read as a "+
			"result (runbook item 9).\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}

	t.Run("mise_tools", func(t *testing.T) {
		listed := section(r.stdout, "=== MISE-LS ===", "=== MISE-INSTALLS ===")
		installs := section(r.stdout, "=== MISE-INSTALLS ===", "=== TIERS ===")
		if strings.Contains(listed, "MISE-LS-FAILED") {
			t.Fatalf("`mise ls --installed` did not run at all. Either `mise` is not on "+
				"the sandbox PATH — which is runbook item 6, the root of this chain, and "+
				"explains this failure rather than being a second bug — or the store at "+
				"%s is unreadable.\n%s", macosuser.SandboxMiseData(""), listed)
		}
		if !strings.Contains(listed, miseTool) {
			t.Errorf("`mise ls --installed` does not name %q, which this workspace's "+
				"`mise_tools` declares. The confined provisioning stage runs `mise "+
				"install` before the agent (macosuser.ProvisionSetup); a tool missing "+
				"here means the stage did not install it, and §10.6's retirement of the "+
				"`mise_tools` warning was premature.\nmise ls --installed:\n%s", miseTool, listed)
		}
		if !strings.Contains(installs, miseTool) {
			t.Errorf("the mise store %s/installs holds no %q directory, so nothing was "+
				"unpacked even if the table above claims it. This is the half that "+
				"distinguishes a recorded install from a real one.\nls:\n%s",
				macosuser.SandboxMiseData(""), miseTool, installs)
		}
	})

	tiers := parseMacosUserTiers(section(r.stdout, "=== TIERS ===", "=== END ==="))
	sidecar := paths.WorkspaceHomeState(ws)

	t.Run("mise_store_is_machine_tier", func(t *testing.T) {
		// THE CONTRAST IS THE TEST. "the store is under /Users/_yolojail" is also true of the
		// pre-12a launch whose HOME was /Users/_yolojail, where it says nothing about tiers.
		// So the session's HOME is asserted first to be THIS workspace's home, and its
		// ~/.yolo/bin a real directory in it: that is what proves the store below sits
		// outside the per-workspace home rather than merely inside some home.
		if home := tiers["home"].target; !samePath(home, sidecar) {
			t.Fatalf("the session's $HOME is %q, want this workspace's home %s. Slice 12a runs "+
				"account mode with HOME = <ws>/.yolo/home (MMP-D25); any other HOME means the "+
				"launch did not take that geometry, so the machine-tier assertion below would "+
				"pass or fail for the wrong reason and is not attempted. Build and launch the "+
				"current dev bundle (`just build-dev-bundle`).", home, sidecar)
		}
		wantBin := filepath.Join(sidecar, ".yolo", "bin")
		if bin := tiers["yolo-bin"]; bin.kind != "dir" || !samePath(bin.target, wantBin) {
			t.Fatalf("~/.yolo/bin is %s, want a real directory at %s. The workspace home "+
				"layout did not apply (entrypoint.DeriveDarwinWorkspaceHome makes it), so the "+
				"machine-tier assertion below is not attempted. Runbook item 5 "+
				"(TestMacosUserHomeTierIsPerWorkspace) is the item for this.",
				bin.describe(), wantBin)
		}
		want := macosuser.SandboxMiseData("")
		if got := tiers["mise-data-dir"].target; got != want {
			t.Errorf("the session's MISE_DATA_DIR is %q, want %s. The launch names it "+
				"absolutely as the machine root's store (macosuser.SandboxMiseData of the "+
				"launch's Machine home, which in account mode is %s); anything else is a store "+
				"no other workspace shares — or, empty, mise's own default under the "+
				"per-workspace ~/.local.", got, want, macosuser.SandboxHome())
		}
		store := tiers["mise-store"]
		switch {
		case store.kind == "missing":
			t.Errorf("%s does not exist (%s). MISE_DATA_DIR names it on both the bootstrap and "+
				"the launch argv (macosuser.SandboxMiseData), and the layout creates it, so an "+
				"absent store means the session's MISE_DATA_DIR is empty or the layout never "+
				"ran — see the mise_tools subtest.", want, store.describe())
		case store.kind == "symlink":
			t.Errorf("%s is a SYMLINK to %s. No link carries the mise store "+
				"(entrypoint.DeriveDarwinWorkspaceHome makes it a directory in the machine "+
				"root), so this one was laid by something else and may lead each workspace to "+
				"its own copy of every tool. Runbook item 9's ⚠ calls this the one thing a "+
				"later launch cannot undo.", want, store.target)
		case !strings.HasPrefix(store.target, macosuser.SandboxHome()+"/"):
			t.Errorf("%s resolves to %s, which is outside the machine root %s. Whatever it "+
				"is, it is not the one store per machine this backend promises.",
				want, store.target, macosuser.SandboxHome())
		case samePath(store.target, sidecar) || strings.HasPrefix(store.target, sidecar+"/"):
			t.Errorf("%s resolves to %s, inside this workspace's home: the mise store landed "+
				"in the PER-WORKSPACE tier, so every workspace gets its own copy of every tool "+
				"and nothing at run time complains.", want, store.target)
		}
	})

	// Where npm-installed programs LAND, asserted on its own: it is a property of the home
	// layout, independent of whether this launch installed anything into the prefix.
	t.Run("npm_prefix_is_workspace_tier", func(t *testing.T) {
		npmPrefix := tiers["npm-global"]
		want := filepath.Join(sidecar, ".npm-global")
		if npmPrefix.kind != "dir" {
			t.Fatalf("~/.npm-global is %s, want a real directory at %s. npm programs are "+
				"installed into this prefix and it is WORKSPACE tier (paths.HomeSurfaces); "+
				"since slice 12a HOME is the workspace home, so the layout makes it a plain "+
				"directory there (MMP-D25), and a link here leads somewhere other workspaces "+
				"can reach.", npmPrefix.describe(), want)
		}
		if !samePath(npmPrefix.target, want) {
			t.Errorf("~/.npm-global resolves to %s, not to this workspace's %s — so the npm "+
				"programs this launch installs land in some OTHER workspace's prefix.",
				npmPrefix.target, want)
		}
	})
}

// macosUserTier is one probed path's shape: whether it is a symlink, a real directory or
// absent, and what it points at (the link target, or the resolved directory).
type macosUserTier struct {
	kind   string // "symlink" | "dir" | "missing" | "" (never probed)
	target string
}

func (e macosUserTier) describe() string {
	switch e.kind {
	case "":
		return "not probed at all — the TIERS section lost its line"
	case "missing":
		return "missing"
	default:
		return e.kind + " -> " + e.target
	}
}

// macosUserToolsProbe is the launch's one probe, fenced. `command -v` is deliberately NOT used
// for the installs: a tool can be installed and still lose a PATH race, and item 9 asks whether
// it ARRIVED. The store is read where MISE_DATA_DIR names it, the variable mise itself reads,
// and TIERS keys each line by a LABEL rather than the expanded path, so an assertion never
// depends on which home the path happens to be spelled under. `home` and `mise-data-dir` are
// values (kind `value`, the variable as the target); the other three are probed paths.
func macosUserToolsProbe() string {
	return strings.Join([]string{
		`echo "=== MISE-LS ==="`,
		`mise ls --installed 2>&1 || echo "MISE-LS-FAILED"`,
		`echo "=== MISE-INSTALLS ==="`,
		`ls -1 "$MISE_DATA_DIR/installs" 2>&1 || echo "MISE-INSTALLS-UNREADABLE"`,
		`echo "=== TIERS ==="`,
		`printf 'home|value|%s\n' "$HOME"`,
		`printf 'mise-data-dir|value|%s\n' "$MISE_DATA_DIR"`,
		`for pair in "mise-store=$MISE_DATA_DIR" "yolo-bin=$HOME/.yolo/bin" "npm-global=$HOME/.npm-global"; do`,
		`  k=${pair%%=*}; p=${pair#*=}`,
		`  if [ -z "$p" ]; then printf '%s|missing|\n' "$k";`,
		`  elif [ -L "$p" ]; then printf '%s|symlink|%s\n' "$k" "$(readlink "$p")";`,
		`  elif [ -d "$p" ]; then printf '%s|dir|%s\n' "$k" "$(cd "$p" && pwd -P)";`,
		`  else printf '%s|missing|\n' "$k"; fi`,
		`done`,
		`echo "=== END ==="`,
	}, "\n")
}

// THE LINUX PREFLIGHT of the probe above, for the home-tier preflight's reason: a shell script
// whose first run would otherwise be on a Mac, after a native floor build. It lays the REAL
// account-mode workspace home (entrypoint.DeriveDarwinWorkspaceHome, HOME the sidecar, one
// machine root as Machine and Shared, MMP-D25), runs the probe with the MISE_DATA_DIR the launch
// sets, and holds the parsed TIERS to the shapes the Mac test asserts. Not behind
// requireMacosUser: it launches nothing, and `mise` need not be installed (its fence is not read).
func TestMacosUserDeclaredToolsProbeReadsARealLayout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH, so the probe script cannot be exercised here")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	machine := filepath.Join(base, "machine")
	home := filepath.Join(base, "ws", ".yolo", "home")
	if err := entrypoint.DeriveDarwinWorkspaceHome(
		entrypoint.DarwinHomes{Home: home, Machine: machine, Shared: machine}, nil, nil).Apply(); err != nil {
		t.Fatalf("applying the real layout: %v", err)
	}
	store := macosuser.SandboxMiseData(machine)
	if err := os.MkdirAll(filepath.Join(store, "installs", "jq"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", macosUserToolsProbe())
	cmd.Env = append(os.Environ(), "HOME="+home, "MISE_DATA_DIR="+store)
	out, _ := cmd.CombinedOutput() // `mise` may be absent here; its fence says so and is not read
	stdout := string(out)
	if got := section(stdout, "=== MISE-INSTALLS ===", "=== TIERS ==="); !strings.Contains(got, "jq") {
		t.Errorf("the probe does not list the store's installs through $MISE_DATA_DIR: %q\n%s", got, stdout)
	}
	tiers := parseMacosUserTiers(section(stdout, "=== TIERS ===", "=== END ==="))
	for k, want := range map[string]macosUserTier{
		"home":          {kind: "value", target: home},
		"mise-data-dir": {kind: "value", target: store},
		"mise-store":    {kind: "dir", target: store},
		"yolo-bin":      {kind: "dir", target: filepath.Join(home, ".yolo", "bin")},
		"npm-global":    {kind: "dir", target: filepath.Join(home, ".npm-global")},
	} {
		if got := tiers[k]; got != want {
			t.Errorf("TIERS %s is %s, want %s: the probe or its parser disagrees with the real "+
				"layout, and the Mac test would fail for a reason that is not the Mac's\n%s",
				k, got.describe(), want.describe(), stdout)
		}
	}
	// THE CONTRAST: a link must read as one, or the Mac test's `dir` assertions prove nothing.
	if err := os.RemoveAll(filepath.Join(home, ".npm-global")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(machine, filepath.Join(home, ".npm-global")); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("bash", "-c", macosUserToolsProbe())
	cmd.Env = append(os.Environ(), "HOME="+home, "MISE_DATA_DIR="+store)
	out, _ = cmd.CombinedOutput()
	if got := parseMacosUserTiers(section(string(out), "=== TIERS ===", "=== END ==="))["npm-global"]; got.kind != "symlink" || got.target != machine {
		t.Errorf("a linked ~/.npm-global reads as %s, want symlink -> %s", got.describe(), machine)
	}
}

// parseMacosUserTiers reads the `label|kind|target` lines the TIERS probe prints.
func parseMacosUserTiers(sectionText string) map[string]macosUserTier {
	out := map[string]macosUserTier{}
	for _, line := range strings.Split(sectionText, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(parts) != 3 || parts[0] == "" {
			continue
		}
		out[parts[0]] = macosUserTier{kind: parts[1], target: parts[2]}
	}
	return out
}

// samePath compares two host-side paths, resolving both through EvalSymlinks first.
//
// It is not string equality because the two spellings arrive from different places: the
// fixture minted its workspace path already resolved (macosUserWorkspace, per AGENTS.md's
// darwin PATH-RESOLUTION rule) while the link target is whatever the launch wrote. A
// path that cannot be resolved falls back to its literal form rather than reporting
// equality, so a missing directory fails the comparison instead of passing it.
func samePath(a, b string) bool {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return a == b || resolve(a) == resolve(b)
}

// readProvisionLog reaches the stage's own record from the HOST side, at the path the
// stage itself writes (provision.StartupLog — one spelling, so a moved log cannot make
// this diagnostic quietly report "no log" forever).
//
// It is READ rather than asserted on: runbook item 7 is what asserts on its contents.
// This is the diagnostic every other item needs when an install is missing and a CI log
// is all the reader has.
func readProvisionLog(workspace string) string {
	b, err := os.ReadFile(provision.StartupLog(workspace))
	if err != nil {
		return fmt.Sprintf("(unreadable: %v — no log at all means the provisioning stage "+
			"never STARTED, which is runbook item 8's case rather than item 9's)", err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "(empty)"
	}
	return string(b)
}
