package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// THE npm ROUTE, ITS NODE FLOOR AND THE UPDATE BOUND, ON macos-user. Three claims the docs carry
// as inferred and unmeasured on this backend (docs/reference/macos-user-provisioning.md, "What
// each imperative config key delivers here"; docs/reference/agent-program-runtimes.md,
// "macos-user: UNVERIFIED"), each given a case here from pinned bytes, so a red is a defect
// rather than a vendor's release:
//
//   - a `via: npm` program installs through its generated launcher, with the floor's node and
//     npm, at the version the pack declared;
//   - its launcher execs it under an absolute node that meets the pack's `node_floor`, which
//     on this backend is meant to be the package floor's node, never a mise shim;
//   - a program's declared update verb runs through the staged yolo's bounded no-terminal verb,
//     in a session of its own, rather than in the launcher's process group, which is where
//     the fallbacks would run it (a stock macOS has no timeout(1)).
//
// They are the installmechanism_test.go cells, re-shaped for this backend: the packs are local
// (`file://`) packs, the npm specimen is the same pinned package, and the installer the update
// cell's program comes from is a file:// script in the WORKSPACE, because a macos-user sandbox
// has no /ctx/packs and cannot read the invoking user's temp dir.
//
// One launch per test: every macos-user launch builds a native floor.

// macosUserNpmFloor is the floor the npm cell declares: met by the package floor's nodejs_24
// with two majors to spare, since a floor is met by any node at or above it. The cell's floor
// check fails only for a node older than 22; a launcher that execs no absolute node, or a mise
// shim, is failed by the check before it.
const macosUserNpmFloor = "22"

func TestMacosUserPinnedNpmProgramInstallsUnderItsNodeFloor(t *testing.T) {
	requireMacosUser(t)
	const packName = "macos-user-npm-fixture"
	pack := resolvedTempDir(t)
	manifest := `{
  "name": "` + packName + `",
  "description": "npm install mechanism and node_floor, pinned, on macos-user",
  "contributes": [
    {"kind": "program", "bin": "` + pinnedNpmBin + `", "via": "npm", "package": "` + pinnedNpmPackage + `",
     "node_floor": "` + macosUserNpmFloor + `"}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+packName+`"}]}`)
	ws := macosUserWorkspace(t, `{}`)

	// The interpreter is read off the launcher's own exec line, the one the generator spliced
	// it into (agent-program-runtimes.md, "The launcher"), and then run: a path is a claim and
	// its --version is the answer.
	launcher := `"$HOME/.yolo/bin/launch/` + pinnedNpmBin + `"`
	r := macosUserRunProbe(t, "npm", ws, strings.Join([]string{
		`echo "=== RESOLVE ==="`, `command -v ` + pinnedNpmBin,
		`echo "=== RUN ==="`, pinnedNpmBin + ` mechanism`,
		`echo "=== VERSION ==="`,
		`jq -r .version "${NPM_CONFIG_PREFIX:-$HOME/.npm-global}/lib/node_modules/` + pinnedNpmBin + `/package.json"`,
		`echo "=== EXEC ==="`, `grep -n 'exec .*REAL_BIN' ` + launcher,
		`echo "=== INTERP ==="`,
		`interp=$(awk '$1=="exec" && $3 ~ /REAL_BIN/ {print $2; exit}' ` + launcher + ` | tr -d "'")`,
		`echo "$interp"`,
		`echo "=== INTERP VERSION ==="`, `"$interp" --version`,
		`echo "=== END ==="`,
	}, "\n"))
	diag := "\nstdout:\n" + r.stdout + "\nstderr:\n" + r.stderr
	if got := strings.TrimSpace(section(r.stdout, "=== RESOLVE ===", "=== RUN ===")); !strings.Contains(got, ".yolo/bin/launch") {
		t.Errorf("%s resolved to %q, want the launcher under ~/.yolo/bin/launch: resolved anywhere "+
			"else, the launcher never ran and nothing was installed%s", pinnedNpmBin, got, diag)
	}
	if got := section(r.stdout, "=== RUN ===", "=== VERSION ==="); !strings.Contains(got, "mechanism") {
		t.Errorf("the installed program did not run: %q%s", got, diag)
	}
	if got := strings.TrimSpace(section(r.stdout, "=== VERSION ===", "=== EXEC ===")); got != pinnedNpmVersion {
		t.Errorf("installed version = %q, want exactly %q, the version the pack declared%s",
			got, pinnedNpmVersion, diag)
	}
	interp := strings.TrimSpace(section(r.stdout, "=== INTERP ===", "=== INTERP VERSION ==="))
	if !filepath.IsAbs(interp) || strings.Contains(interp, "/shims/") {
		t.Errorf("the launcher execs %q, want an absolute node and never a mise shim: a floor the "+
			"generator resolved splices one in front of $REAL_BIN%s", interp, diag)
	}
	v := strings.TrimPrefix(strings.TrimSpace(section(r.stdout, "=== INTERP VERSION ===", "=== END ===")), "v")
	if !packdecl.SatisfiesNodeFloor(v, macosUserNpmFloor) {
		t.Errorf("the launcher execs %s, version %q, which does not meet the floor %s%s",
			interp, v, macosUserNpmFloor, diag)
	}
	t.Logf("MEASUREMENT: the %s launcher execs %s (node %s) for node_floor %s", pinnedNpmBin, interp, v,
		macosUserNpmFloor)
}

func TestMacosUserBoundsAnUpdateVerbThroughTheStagedYolo(t *testing.T) {
	requireMacosUser(t)
	const (
		packName = "macos-user-update-fixture"
		bin      = "yolo-update-fixture"
		sentinel = "update-fixture-ran"
	)
	pack := resolvedTempDir(t)
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+packName+`"}]}`)
	ws := macosUserWorkspace(t, `{}`)

	// The installed tool's `selfupdate` records how it was run: whether it leads its own
	// process group (the no-terminal verb starts it in a session of its own; both fallbacks run
	// it in the launcher's group), whether its stdin is a terminal, and its parent's command
	// name, which is the verb's when the bound is yolo's. ps answers for a process of the same
	// sandbox (the profile's same-sandbox re-allow).
	installer := `#!/bin/bash
set -euo pipefail
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/` + bin + `" <<'TOOL'
#!/bin/bash
if [ "${1:-}" = selfupdate ]; then
  pgid=$(ps -o pgid= -p $$ | tr -d ' ')
  printf 'leader=%s stdin_tty=%s\n' \
    "$([ "$pgid" = "$$" ] && echo yes || echo no)" \
    "$([ -t 0 ] && echo yes || echo no)" > "$HOME/.local/update-run"
  printf 'pid=%s pgid=%s parent=%s\n' "$$" "$pgid" "$(ps -o comm= -p $PPID)" >> "$HOME/.local/update-run"
  exit 0
fi
echo "` + sentinel + `"
TOOL
chmod +x "$HOME/.local/bin/` + bin + `"
`
	if err := os.WriteFile(filepath.Join(ws, "install.sh"), []byte(installer), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "` + packName + `",
  "description": "the update bound on macos-user",
  "contributes": [
    {"kind": "program", "bin": "` + bin + `", "via": "installer",
     "url": "file://` + filepath.Join(ws, "install.sh") + `", "update": ["selfupdate"]}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// The first run installs and stamps; backdating the stamp past UPDATE_INTERVAL makes the
	// second run the update. The stamp dir is read off the launcher, which bakes it.
	r := macosUserRunProbe(t, "update-bound", ws, strings.Join([]string{
		bin,
		`eval "$(grep '^STAMP_DIR=' "$HOME/.yolo/bin/launch/` + bin + `")"`,
		`touch -t 200001010000 "$STAMP_DIR/` + bin + `.stamp"`,
		`echo "=== SECOND ==="`, bin + ` 2>&1`,
		`echo "=== HOW ==="`, `cat "$HOME/.local/update-run"`,
		`echo "=== END ==="`,
	}, "\n"))
	diag := "\nstdout:\n" + r.stdout + "\nstderr:\n" + r.stderr
	second := section(r.stdout, "=== SECOND ===", "=== HOW ===")
	if !strings.Contains(second, "Updating "+bin+"...") {
		t.Errorf("the second run did not take the update path%s", diag)
	}
	if strings.Contains(r.combined(), "cannot detach") {
		t.Errorf("the update took a fallback branch, not the staged yolo's no-terminal verb%s", diag)
	}
	if !strings.Contains(second, sentinel) {
		t.Errorf("the program did not run after its update%s", diag)
	}
	how := section(r.stdout, "=== HOW ===", "=== END ===")
	first, _, _ := strings.Cut(strings.TrimSpace(how), "\n")
	if first != "leader=yes stdin_tty=no" {
		t.Errorf("the update verb ran as %q, want a process group of its own and no terminal on its "+
			"stdin%s", first, diag)
	}
	t.Logf("MEASUREMENT: the update verb ran as:\n%s", strings.TrimSpace(how))
}
