package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// RUNBOOK ITEM 9 — `mise_tools` and `lsp_servers` actually arrive
// (docs/plans/runbooks/macos-user-manual-checks.md).
//
// WHAT IT SETTLES. Two launch warnings were RETIRED on 2026-09-12 — `mise_tools` and
// `lsp_servers` each used to say "this backend installs nothing" — and both were removed
// on the strength of code that had never run
// (docs/design/macos-user-provisioning.md §10.6, whose own ⚠ says *"the absence of a
// warning is not evidence of a feature"*). This is the evidence. If a declared tool is
// absent here, that retirement was premature and the warning it deleted should come back.
//
// THE TIER CHECK IS THE HALF THAT CANNOT BE UNDONE BY A LATER LAUNCH, which is why it is
// asserted beside the installs rather than left to item 5. `~/.yolo/mise` is the MACHINE
// tier: one tool store for every workspace, the way the container mounts one /mise
// (macosuser.SandboxMiseData states the rule). Its siblings `~/.yolo/bin` and
// `~/.npm-global` are the WORKSPACE tier and are symlinks into `<ws>/.yolo/home`. Getting
// that backwards gives every workspace its own copy of every tool — the inverse of every other
// backend — and nothing at run time complains.
//
// IT RUNS ONE LAUNCH AND SPLITS THE ANSWER INTO SUBTESTS, on this suite's fencing
// convention: a macos-user launch builds a native nix closure and then installs from the
// network, so asking four questions in four launches would cost four of those. A subtest
// inherits the TestMacosUser… prefix the gate requires, so the CI filter still selects it.
//
// ⚠ ONE SUBTEST IS PREDICTED TO FAIL ON THE FIRST MAC RUN — `lsp_servers`, and the
// prediction is a SOURCE READING made on 2026-09-12 while writing this test, never a
// measurement. NOTHING on this backend sets YOLO_LSP_NPM_INSTALL or YOLO_LSP_GO_INSTALL,
// the two variables the generated bootstrap script's install loop reads
// (internal/entrypoint/shell.go); their only producer in the tree is the container's
// podman argv (internal/cli/run/assemble.go). So the stage execs the script, the loop
// iterates an empty list, and the stage exits 0 having installed no LSP server.
//
// The assertion is written against the SPEC rather than against that reading, which is
// the point: a Mac is what turns the prediction into a fact either way. IF IT PASSES,
// THIS PARAGRAPH IS WHAT IS WRONG — delete it, and leave the test.
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
	// The declared LSP server is `python`, whose recipe is the single npm package
	// `pyright` (internal/cli/run/lsp.go lspInstallRecipes). It is the cheapest entry in
	// that table — one package, no `go install`, no compiler — and `command`/
	// `fileExtensions` are spelled because config.validateLSPServers requires both.
	const lspServer = "python"
	const lspBin = "pyright-langserver"
	wsConfig := fmt.Sprintf(`{
  "mise_tools": {%q: "latest"},
  "lsp_servers": {
    %q: {"command": %q, "args": ["--stdio"], "fileExtensions": {".py": "python"}}
  }
}`, miseTool, lspServer, lspBin)

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

	// One probe, fenced. `command -v` is deliberately NOT used for the installs: a tool
	// can be installed and still lose a PATH race, and item 9 asks whether it ARRIVED.
	probe := strings.Join([]string{
		`echo "=== MISE-LS ==="`,
		`mise ls --installed 2>&1 || echo "MISE-LS-FAILED"`,
		`echo "=== MISE-INSTALLS ==="`,
		`ls -1 "$HOME/.yolo/mise/installs" 2>&1 || echo "MISE-INSTALLS-UNREADABLE"`,
		`echo "=== TIERS ==="`,
		`for p in "$HOME/.yolo/mise" "$HOME/.yolo/bin" "$HOME/.npm-global"; do`,
		`  if [ -L "$p" ]; then printf '%s|symlink|%s\n' "$p" "$(readlink "$p")";`,
		`  elif [ -d "$p" ]; then printf '%s|dir|%s\n' "$p" "$(cd "$p" && pwd -P)";`,
		`  else printf '%s|missing|\n' "$p"; fi`,
		`done`,
		`echo "=== NPMBIN ==="`,
		`ls -1 "$HOME/.npm-global/bin" 2>&1 || echo "NPMBIN-UNREADABLE"`,
		`echo "=== END ==="`,
	}, "\n")

	r := runMacosUser(t, ws, probe)
	if r.rc != 0 {
		t.Fatalf("the macos-user launch failed (rc %d) before any probe could answer "+
			"(runbook item 9). A non-zero rc here is the PROVISIONING VETO or a launch "+
			"fault, not a missing tool — read the log below.\nstdout:\n%s\nstderr:\n%s\n"+
			"%s:\n%s", r.rc, r.stdout, r.stderr, provision.StartupLog(ws), readProvisionLog(ws))
	}
	if strings.TrimSpace(section(r.stdout, "=== TIERS ===", "=== NPMBIN ===")) == "" {
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

	tiers := parseMacosUserTiers(section(r.stdout, "=== TIERS ===", "=== NPMBIN ==="))
	sidecar := paths.WorkspaceHomeState(ws)

	t.Run("mise_store_is_machine_tier", func(t *testing.T) {
		// THE CONTRAST IS THE TEST. "~/.yolo/mise is not a symlink" passes vacuously on a
		// launch where the layout never ran at all, so its sibling — which MUST be a
		// symlink — is asserted first: that is what proves the probe can tell the two
		// apart on this machine.
		bin := tiers[macosuser.SandboxHome()+"/.yolo/bin"]
		if bin.kind != "symlink" {
			t.Fatalf("~/.yolo/bin is %q, not a symlink into the workspace sidecar. The "+
				"per-workspace home layout did not apply (entrypoint.DeriveDarwinHomeLayout "+
				"links it), so the machine-tier assertion below would pass for the wrong "+
				"reason and is not attempted. Runbook item 5 is the item for this.",
				bin.describe())
		}
		store := tiers[macosuser.SandboxMiseData("")]
		switch {
		case store.kind == "missing":
			t.Errorf("%s does not exist. MISE_DATA_DIR names it on both the bootstrap and "+
				"the launch argv (macosuser.SandboxMiseData), so an absent store means "+
				"nothing ever ran `mise install` — see the mise_tools subtest.",
				macosuser.SandboxMiseData(""))
		case store.kind == "symlink":
			t.Errorf("%s is a SYMLINK to %s — the mise store landed in the PER-WORKSPACE "+
				"tier. Every other backend keeps ONE tool store per machine (the container "+
				"mounts one /mise for every workspace), so this gives each workspace its "+
				"own copy of every tool and nothing at run time complains. Runbook item 9's "+
				"⚠ calls this the one thing a later launch cannot undo.",
				macosuser.SandboxMiseData(""), store.target)
		case !strings.HasPrefix(store.target, macosuser.SandboxHome()+"/"):
			t.Errorf("%s resolves to %s, which is outside the sandbox account home %s. "+
				"Whatever it is, it is not the machine tier this backend promises.",
				macosuser.SandboxMiseData(""), store.target, macosuser.SandboxHome())
		}
	})

	// SEPARATE FROM lsp_servers ON PURPOSE: where the binaries LAND and whether they were
	// installed are two verdicts, and a Mac reader with only a CI log needs to be able to
	// tell "installed into the wrong prefix" from "never installed".
	t.Run("npm_prefix_is_workspace_tier", func(t *testing.T) {
		npmPrefix := tiers[macosuser.SandboxHome()+"/.npm-global"]
		if npmPrefix.kind != "symlink" {
			t.Fatalf("~/.npm-global is %q, not a symlink into %s. The LSP binaries are "+
				"installed into this prefix and it is WORKSPACE tier "+
				"(paths.HomeSurfaces), so a real directory here is one npm prefix shared "+
				"by every workspace on the machine.", npmPrefix.describe(), sidecar)
		}
		if want := filepath.Join(sidecar, "npm-global"); !samePath(npmPrefix.target, want) {
			t.Errorf("~/.npm-global points at %s, not at this workspace's sidecar %s — so "+
				"the LSP servers this launch installed land in some OTHER workspace's "+
				"prefix.", npmPrefix.target, want)
		}
	})

	t.Run("lsp_servers", func(t *testing.T) {
		bins := section(r.stdout, "=== NPMBIN ===", "=== END ===")
		if strings.Contains(bins, "NPMBIN-UNREADABLE") {
			t.Fatalf("~/.npm-global/bin could not be listed at all:\n%s", bins)
		}
		if !lineSetHas(bins, lspBin) {
			t.Errorf("`lsp_servers` declares %q and ~/.npm-global/bin holds no %q, so the "+
				"server was never installed (runbook item 9).\n\n"+
				"WHAT TO CHECK FIRST, because this was predicted from source when the test "+
				"was written and never measured: the generated bootstrap script's install "+
				"loop reads $YOLO_LSP_NPM_INSTALL and $YOLO_LSP_GO_INSTALL "+
				"(internal/entrypoint/shell.go), and the ONLY producer of either in the "+
				"tree is the container's podman argv (internal/cli/run/assemble.go). "+
				"Neither the macos-user bootstrap env (macosuser.buildBootstrapEnv) nor "+
				"the provisioning stage env (macosuser.ProvisionArgv, which takes the "+
				"launch env) carries them — so the stage execs the script, the loop "+
				"iterates an empty list, and the stage exits 0 having installed nothing. "+
				"If that is what the log below shows, §10.6 retired the `lsp_servers` "+
				"warning over a gap that was never closed.\n\n"+
				"ls ~/.npm-global/bin:\n%s", lspServer, lspBin, bins)
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

// parseMacosUserTiers reads the `path|kind|target` lines the TIERS probe prints.
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

// lineSetHas reports whether one whole line of `ls -1` output is name. Substring matching
// would accept `pyright-langserver-shim` for `pyright-langserver`, which is the kind of
// near-miss an installer really does leave behind.
func lineSetHas(lsOutput, name string) bool {
	for _, line := range strings.Split(lsOutput, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
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
