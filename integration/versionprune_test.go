package integration

// versionprune_test.go is the launch tier of L7 (docs/design/disk-levers-and-backfill.md §3,
// BF-D1): the native launcher a real jail generates for claude prunes its superseded versions on
// a run that installs nothing and updates nothing. The unit cells
// (internal/entrypoint/versionprune_test.go) run the template's body against a fake program; only
// a launch proves the entrypoint the jail mounts generates a launcher carrying the call, for a pack
// whose versions directory is the default one.
//
// No agent runs: the versions are fake two-line scripts, the policy is frozen so no update verb
// runs, and the launcher is invoked with YOLO_INSTALL_ONLY=1, which exits right after the
// install-or-update step and the prune, before anything that would exec the program.

import (
	"strings"
	"testing"
)

func TestTheClaudeLauncherPrunesSupersededVersionsWithNoUpdate(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	// agent_updates is user scope, and false bakes the update branch out of the launcher, so
	// the only act that can change the version tree below is the every-invocation prune.
	packHome(t, `{"packs": ["claude"], "agent_updates": false}`)

	script := strings.Join([]string{
		`set -e`,
		`vd="$HOME/.local/share/claude/versions"`,
		`mkdir -p "$vd" "$HOME/.local/bin"`,
		// Oldest first, an hour apart, so "newest two" is decidable by mtime.
		`t=$(( $(date +%s) - 36000 ))`,
		`for v in 2.1.165 2.1.218 2.1.219 2.1.220 2.1.260; do ` +
			`printf '#!/bin/sh\necho fake\n' > "$vd/$v"; chmod +x "$vd/$v"; ` +
			`t=$((t + 3600)); touch -d "@$t" "$vd/$v"; done`,
		`ln -sfn "$vd/2.1.260" "$HOME/.local/bin/claude"`,
		`set +e`,
		`YOLO_INSTALL_ONLY=1 "$HOME/.yolo/bin/launch/claude"; echo "launcher_rc=$?"`,
		`echo "=== LEFT ==="`,
		`ls "$vd"`,
		`echo "=== END ==="`,
	}, "\n")
	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("probe script failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.stdout, "launcher_rc=0\n") {
		t.Fatalf("the install-only launcher must exit 0 over an executable REAL_BIN:\n%s", r.combined())
	}
	left := strings.Fields(section(r.stdout, "=== LEFT ===", "=== END ==="))
	if strings.Join(left, ",") != "2.1.220,2.1.260" {
		t.Errorf("a launcher run with no install and no update left %v, want the newest two "+
			"[2.1.220 2.1.260]\n%s", left, r.combined())
	}
	if !strings.Contains(r.combined(), "claude: removed superseded version 2.1.165") {
		t.Errorf("the prune must name what it removed:\n%s", r.combined())
	}
}
