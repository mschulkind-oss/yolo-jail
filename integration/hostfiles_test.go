package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

// host_files end-to-end tests (docs/plans/host-file-staging.md).
//
// SCOPE NOTE — why these cover the source-LESS half only. A source-bearing entry
// (bare string, or an object with `source`) is read ONLY from
// ~/.config/yolo-jail/config.jsonc, by construction: that is the credential
// boundary (config.LoadHostFiles). Exercising one from a test would mean writing
// the developer's real user config, which in this repo's own jail is a :ro bind of
// the maintainer's dotfiles — so these tests drive the half that is legal at
// workspace scope, and additionally assert that a source-bearing entry at
// workspace scope is REJECTED, which is the boundary's observable behavior.
// The source-bearing render path itself is covered by
// internal/entrypoint/hostfiles_test.go (against a fake /ctx/host-user mount) and
// its mount emission by internal/cli/run/hostfiles_test.go.

// TestHostFilesSourceLessModes drives every source-less mode through a real jail
// in ONE launch: `once` seeds and then survives an in-jail edit, `copy` is
// regenerated, a `managed` key reverts while sibling edits are captured, and the
// destination lands writable even under a brand-new directory. Merged into a
// single launch to pay the container cold-start once.
func TestHostFilesSourceLessModes(t *testing.T) {
	requireJail(t)

	dir := writeProjectWithPacks(t, `{
  "network": {"mode": "bridge"},
  "host_files": [
    {"path": "~/.config/yolo-it/seeded.conf", "content": "seeded=1\n"},
    {"path": "~/.config/yolo-it/copied.json", "mode": "copy",
     "defaults": {"regenerated": true}},
    {"path": "~/.config/yolo-it/managed.json", "mode": "capture",
     "defaults": {"telemetry": false, "theme": "dark"},
     "managed": {"telemetry": false}},
    {"path": "~/yolo-it-newdir/nested.conf", "content": "newdir=1\n"}
  ]
}`, "claude")

	// Boot 1: every entry must render. Then edit each one so boot 2 can prove the
	// per-mode behavior.
	first := runYolo(t, dir, strings.Join([]string{
		`echo "=== SEEDED ==="; cat ~/.config/yolo-it/seeded.conf`,
		`echo "=== COPIED ==="; cat ~/.config/yolo-it/copied.json`,
		`echo "=== MANAGED ==="; cat ~/.config/yolo-it/managed.json`,
		`echo "=== NEWDIR ==="; cat ~/yolo-it-newdir/nested.conf`,
		// In-jail edits, one per mode.
		`echo "edited=1" > ~/.config/yolo-it/seeded.conf`,
		`echo '{"regenerated":false}' > ~/.config/yolo-it/copied.json`,
		`printf '{"telemetry":true,"theme":"light"}' > ~/.config/yolo-it/managed.json`,
		`echo "=== DONE ==="`,
	}, "; "))
	if first.rc != 0 {
		t.Fatalf("boot 1 rc=%d\nstdout:\n%s\nstderr:\n%s", first.rc, first.stdout, first.stderr)
	}
	if got := section(first.combined(), "=== SEEDED ===", "=== COPIED ==="); !strings.Contains(got, "seeded=1") {
		t.Errorf("`once` entry did not seed from content: %q", got)
	}
	if got := section(first.combined(), "=== COPIED ===", "=== MANAGED ==="); !strings.Contains(got, "regenerated") {
		t.Errorf("`copy` entry did not render from defaults: %q", got)
	}
	if got := section(first.combined(), "=== MANAGED ===", "=== NEWDIR ==="); !strings.Contains(got, "telemetry") {
		t.Errorf("`capture` entry did not render: %q", got)
	}
	// The new top-level dir is the case that EROFS-failed before the CLI staged a
	// writable subtree for it.
	if got := section(first.combined(), "=== NEWDIR ===", "=== DONE ==="); !strings.Contains(got, "newdir=1") {
		t.Errorf("destination under a NEW top-level dir did not render "+
			"(writable-subtree staging missing?): %q", got)
	}

	// Boot 2: the modes must now diverge.
	second := runYolo(t, dir, strings.Join([]string{
		`echo "=== SEEDED2 ==="; cat ~/.config/yolo-it/seeded.conf`,
		`echo "=== COPIED2 ==="; cat ~/.config/yolo-it/copied.json`,
		`echo "=== MANAGED2 ==="; cat ~/.config/yolo-it/managed.json`,
		`echo "=== DONE2 ==="`,
	}, "; "), withTimeout(jailTimeout()))
	if second.rc != 0 {
		t.Fatalf("boot 2 rc=%d\nstdout:\n%s\nstderr:\n%s", second.rc, second.stdout, second.stderr)
	}
	out := second.combined()

	// `once`: seeded then never touched, so the edit persists verbatim.
	if got := section(out, "=== SEEDED2 ===", "=== COPIED2 ==="); !strings.Contains(got, "edited=1") {
		t.Errorf("`once` re-seeded over an in-jail edit (must leave it alone): %q", got)
	}
	// `copy`: regenerated every boot, so the edit is gone.
	if got := section(out, "=== COPIED2 ===", "=== MANAGED2 ==="); strings.Contains(got, "false") {
		t.Errorf("`copy` preserved an in-jail edit (must overwrite): %q", got)
	}
	// `capture`: the managed key reverts, the non-managed edit survives.
	managed := section(out, "=== MANAGED2 ===", "=== DONE2 ===")
	if strings.Contains(managed, `"telemetry": true`) {
		t.Errorf("managed key did not revert on `capture`: %q", managed)
	}
	if !strings.Contains(managed, "light") {
		t.Errorf("`capture` lost the non-managed in-jail edit (theme): %q", managed)
	}
}

// TestHostFilesConfigLsAndReset drives the Phase-3 visibility commands against a
// real jail: `config ls` must flag exactly the capture surface that diverged, and
// `config reset` must clear it. These run host-side against the workspace the jail
// wrote its sidecars into, so they exercise the same sidecar layout the entrypoint
// produced rather than a fixture.
//
// Being host-side is also why the reset half needs --force: the write guard refuses
// off the jail that owns the workspace. See the comment at that call.
func TestHostFilesConfigLsAndReset(t *testing.T) {
	requireJail(t)

	dir := writeProjectWithPacks(t, `{
  "network": {"mode": "bridge"},
  "host_files": [
    {"path": "~/.config/yolo-it/cap.json", "mode": "capture",
     "defaults": {"a": 1}},
    {"path": "~/.config/yolo-it/plain.conf", "content": "plain\n"}
  ]
}`, "claude")

	// Boot 1 renders both; then edit the capture surface so it diverges.
	if r := runYolo(t, dir, `printf '{"a":1,"mine":true}' > ~/.config/yolo-it/cap.json`); r.rc != 0 {
		t.Fatalf("boot 1 rc=%d\n%s\n%s", r.rc, r.stdout, r.stderr)
	}
	// Boot 2 captures that edit into the overlay sidecar.
	boot2 := runYolo(t, dir, `cat ~/.config/yolo-it/cap.json`)
	if boot2.rc != 0 {
		t.Fatalf("boot 2 rc=%d\n%s\n%s", boot2.rc, boot2.stdout, boot2.stderr)
	}
	if !strings.Contains(boot2.combined(), "mine") {
		t.Fatalf("capture surface lost the in-jail edit, so there is nothing to inspect:\n%s",
			boot2.combined())
	}
	// The boot notice must announce the divergence.
	if !strings.Contains(boot2.combined(), "captured in-jail edits") {
		t.Errorf("boot printed no divergence notice for a non-empty overlay:\n%s", boot2.stderr)
	}

	// `config ls` must flag the capture surface — and only it.
	ls := runYoloCLI(t, dir, "config", "ls")
	if ls.rc != 0 {
		t.Fatalf("config ls rc=%d\n%s\n%s", ls.rc, ls.stdout, ls.stderr)
	}
	if !strings.Contains(ls.stdout, "captured in-jail edits") {
		t.Errorf("config ls did not flag the diverged surface:\n%s", ls.stdout)
	}
	for _, line := range strings.Split(ls.stdout, "\n") {
		if strings.Contains(line, "plain.conf") && strings.Contains(line, "⚠") {
			t.Errorf("a no-sidecar (`once`) surface was flagged as diverged: %q", line)
		}
	}

	// `config diff user` must show the captured key.
	diff := runYoloCLI(t, dir, "config", "diff", "user")
	if diff.rc != 0 {
		t.Fatalf("config diff rc=%d\n%s\n%s", diff.rc, diff.stdout, diff.stderr)
	}
	if !strings.Contains(diff.stdout, "mine") {
		t.Errorf("config diff did not show the captured key:\n%s", diff.stdout)
	}

	// `config reset user` needs --force HERE, and that is the product behaving
	// correctly — do not "fix" this by dropping the flag.
	//
	// reset does two things: delete the sidecars, and truncate the surface FILE to its
	// pure render. The second half resolves `~` against the INVOKING process's home, so
	// it is only safe in the jail that owns the workspace. refuseHostSideWrite() gates
	// on exactly that (surfacesAreLocal(): in-jail AND the resolved workspace is
	// /workspace), because otherwise reset truncates a real dotfile yolo does not own
	// there.
	//
	// This harness is permanently on the refused side of that gate and cannot be moved
	// to the other one: it drives the host-side CLI against a TEMP workspace, so the
	// surfaces it would truncate live in the inner jail's home while `~` resolves to the
	// harness's own home. That is the guard's target case, not an exemption — hence the
	// documented escape hatch. It is safe here only because packHome() has already
	// pointed HOME at a temp dir, where the destination does not exist (reset leaves an
	// absent surface absent), so nothing outside the fixture is written.
	//
	// First assert the guard actually fires, so this test also pins the refusal
	// end-to-end rather than just tunnelling through it.
	refused := runYoloCLI(t, dir, "config", "reset", "user")
	if refused.rc == 0 {
		t.Errorf("config reset without --force should refuse off the owning jail, got rc=0:\n%s",
			refused.combined())
	}
	if before := captureSidecars(dir); len(before) == 0 {
		t.Errorf("the refused reset removed the sidecars — it must touch nothing: %v", before)
	}

	// Now the real assertion: with --force, reset clears the sidecars.
	reset := runYoloCLI(t, dir, "config", "reset", "user", "--force")
	if reset.rc != 0 {
		t.Fatalf("config reset rc=%d\n%s\n%s", reset.rc, reset.stdout, reset.stderr)
	}
	if sidecars := captureSidecars(dir); len(sidecars) != 0 {
		t.Errorf("config reset left user capture sidecars behind: %v", sidecars)
	}
	// And `ls` must now be clean.
	ls2 := runYoloCLI(t, dir, "config", "ls")
	if strings.Contains(ls2.stdout, "captured in-jail edits") {
		t.Errorf("config ls still reports divergence after reset:\n%s", ls2.stdout)
	}
}

// captureSidecars lists the user-surface sidecars that carry CAPTURE STATE — the
// overlay (the captured edits) and the last_render (the baseline they are diffed
// against). Those two are exactly what `config reset` removes, and both must be
// gone for a reset to have taken effect: dropping only the overlay would leave the
// next boot re-capturing the discarded edits against a stale baseline.
//
// The `.provenance` sidecar is deliberately EXCLUDED, and reset leaving it is not a
// leak. It is a per-boot observability record ("which layer set each key"), written
// unconditionally by every render and read by nothing in the reset/diff/capture path,
// so it holds no captured edit to discard and the next boot rewrites it wholesale.
// A bare `user-*` glob here asserted otherwise and went red when Phase 2 added the
// file — the assertion predated it, so it was over-broad rather than newly violated.
func captureSidecars(workspaceDir string) []string {
	var out []string
	for _, suffix := range []string{"*.overlay.json", "*.last_render"} {
		found, _ := filepath.Glob(filepath.Join(workspaceDir, ".yolo", "prism", "user-"+suffix))
		out = append(out, found...)
	}
	return out
}

// TestHostFilesWorkspaceScopeSourceBearingRejected is the credential boundary's
// observable half: a workspace config travels with the repo and is agent-editable,
// so it must never be able to decide which HOST files cross into the jail. The
// error must arrive from `yolo check` (and block a run) rather than being a silent
// no-op, which is what it would otherwise be — LoadHostFiles ignores such an entry
// entirely.
func TestHostFilesWorkspaceScopeSourceBearingRejected(t *testing.T) {
	requireJail(t)

	// A bare string is always source-bearing. It must be written "~/…" — an
	// absolute path is rejected earlier, as a path error, which would make this
	// test pass for the wrong reason. Point at a real host dotfile so the rejection
	// also cannot be confused with a missing-source complaint.
	dir := writeProjectWithPacks(t, `{
  "host_files": ["~/.bashrc-yolo-it-probe"]
}`, "claude")

	check := runYoloCLI(t, dir, "check", "--no-build")
	if check.rc == 0 {
		t.Fatalf("workspace-scope source-bearing entry passed `yolo check`:\n%s", check.combined())
	}
	if !strings.Contains(check.combined(), "user-scope only") {
		t.Errorf("check rejected the entry but not with the scope explanation:\n%s", check.combined())
	}
}

// TestHostFilesReservedDestinationRejected: an entry may not clobber a file yolo
// owns. Composing over ~/.claude/settings.json would render the same file the prism
// renders, and whichever ran last would win — quietly stripping yolo's managed
// block (the jail's whole permission posture).
func TestHostFilesReservedDestinationRejected(t *testing.T) {
	requireJail(t)

	dir := writeProjectWithPacks(t, `{
  "host_files": [
    {"path": "~/.claude/settings.json", "content": "{}"}
  ]
}`, "claude")

	check := runYoloCLI(t, dir, "check", "--no-build")
	if check.rc == 0 {
		t.Fatalf("entry targeting a builtin surface passed `yolo check`:\n%s", check.combined())
	}
	if !strings.Contains(check.combined(), "managed by yolo") {
		t.Errorf("rejection lacked the clobber explanation:\n%s", check.combined())
	}
}
