package integration

import (
	"os"
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
//
// THE "developer's real user config" OBSTACLE IS GONE as of the harness's default HOME
// isolation (2026-08-21, storage-and-config.md §10.5): the user config a container test
// writes is a temp file in a temp home, and the "host home" a source-bearing entry reads
// from is that same temp home — so such an entry IS testable now, seeding its own source
// file. Nobody has written that test; the split above describes where the coverage is,
// and is no longer a limit on where it could be.

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
// Being host-side is also the POINT of the reset half since [OQ-CR4]: a host-side reset of a
// stopped jail's captured edits is the fourth disposition, and it is the only exit from a
// captured edit that does not require launching the jail whose capture you are discarding.
// See the comment at that call.
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

	// `config reset user` NEEDS NO --force HERE, and that is the whole of [OQ-CR4]
	// (docs/design/config-target-resolution.md) — do not "fix" this by adding the flag back.
	//
	// It used to refuse, on the premise that reset truncates a surface file resolved against
	// the INVOKING process's home. That premise is true of a real home and false of the one
	// this resolves: a workspace target finds the jail's own home overlay,
	// <workspace>/.yolo/home/…, which is the same inode the jail writes. The refusal cost a
	// real capability — the only exit from a captured edit was the verb inside the owning
	// jail, so discarding a STOPPED jail's captures meant launching it, and a launch renders
	// and captures first.
	//
	// What remains is an ORDERING condition, not a weaker guard: a reset refuses while a jail
	// for that workspace is RUNNING (and while the runtime cannot be asked whether it is).
	// This harness's jail has exited by now, which is the disposition under test. The running
	// and unqueryable arms are unit-pinned, in internal/cli/confighostjailreset_test.go,
	// because they need a stubbed runtime probe.
	reset := runYoloCLI(t, dir, "config", "reset", "user")
	if reset.rc != 0 {
		t.Fatalf("config reset rc=%d\n%s\n%s", reset.rc, reset.stdout, reset.stderr)
	}
	if overlays := captureOverlays(dir); len(overlays) != 0 {
		t.Errorf("config reset left user capture overlays behind: %v", overlays)
	}
	// AND IT TRUNCATED THE JAIL'S OWN FILE, which is the half deleting sidecars does not do:
	// without it the next boot finds no baseline, takes the first-migration branch and ADOPTS
	// the file, so the discarded edit comes straight back. A `user` surface reaches this only
	// because its codec and layers now come from the matching `host_files` entry.
	surface := filepath.Join(dir, ".yolo", "home", "config", "yolo-it", "cap.json")
	if data, err := os.ReadFile(surface); err != nil {
		t.Errorf("read the jail's own copy after reset: %v", err)
	} else if strings.Contains(string(data), "mine") {
		t.Errorf("reset discarded the sidecars and left the edit in the jail's file — half an "+
			"undo, and the next launch adopts it back:\n%s", data)
	} else if baseline, berr := os.ReadFile(filepath.Join(dir, ".yolo", "prism",
		"user-"+userCapSlug+".last_render")); berr != nil {
		// RE-SEEDED, NOT DELETED (OQ-CO7 D1, reseedResetBaseline): reset has just written
		// these bytes, so it writes the baseline for them rather than leaving the next render
		// to infer one — which is how a reset once spent the one-per-surface adoption archive
		// on a copy of yolo's own output. A truncation therefore leaves ONE sidecar, and it
		// is the un-stale one.
		t.Errorf("reset truncated the surface and left no baseline for those bytes: %v", berr)
	} else if string(baseline) != string(data) {
		t.Errorf("the re-seeded baseline is not the file reset wrote:\n baseline: %q\n file: "+
			"    %q\n\nThe next render diffs the two and captures the difference as a user edit.",
			baseline, data)
	}
	// And `ls` must now be clean.
	ls2 := runYoloCLI(t, dir, "config", "ls")
	if strings.Contains(ls2.stdout, "captured in-jail edits") {
		t.Errorf("config ls still reports divergence after reset:\n%s", ls2.stdout)
	}
}

// userCapSlug is the surface Name the fixture's `~/.config/yolo-it/cap.json` entry lowers to
// (config.HostFileEntry.Slug: `.`, `-` and alphanumerics pass through, everything else is
// percent-escaped). Spelled here rather than derived so this file needs no config import.
const userCapSlug = ".config_2fyolo-it_2fcap.json"

// captureOverlays lists the user-surface CAPTURE OVERLAYS — the captured edits themselves,
// which is what `config reset` discards.
//
// ⚠ IT USED TO LIST THE BASELINE TOO, and required both to be gone. That was right while a
// `user` surface had no codec and reset could not truncate one: nothing was written, so no
// baseline was true. Now that reset truncates the file it RE-SEEDS the baseline for the bytes
// it just wrote (reseedResetBaseline) — the stale-baseline hazard the old assertion was
// reaching for is prevented by a stronger statement, asserted at the call: last_render must
// equal what is on disk.
//
// The `.provenance` sidecar is deliberately EXCLUDED, and reset leaving it is not a
// leak. It is a per-boot observability record ("which layer set each key"), written
// unconditionally by every render and read by nothing in the reset/diff/capture path,
// so it holds no captured edit to discard and the next boot rewrites it wholesale.
// A bare `user-*` glob here asserted otherwise and went red when Phase 2 added the
// file — the assertion predated it, so it was over-broad rather than newly violated.
func captureOverlays(workspaceDir string) []string {
	var out []string
	for _, suffix := range []string{"*.overlay.json"} {
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

// TestConfigTargetResolvesFromTheCwd is the pin the config-target plan names as the one
// test that would catch the whole resolution breaking, and that the unit tests would not:
// they drive `configRunW` with the cwd under their own control, so a resolution that
// silently answered about the wrong home would satisfy every one of them.
//
// The ruled behaviour, from config-target-resolution.md's ledger, is a PAIR — the same
// binary, two directories, two different and correctly-named answers:
//
//   - in a workspace: that workspace at the `jail` notch (OQ-CR1, ruled (a): the cwd
//     selects the target, and what the design removed was the predicate PAIR rather than
//     the inference);
//   - outside one: the HOST notch, disclosed and at rc 0 — an answer, not a refusal
//     (OQ-CR2). A confident empty answer was the original defect, so the failure this
//     guards against is silence, not an error.
//
// It runs the CLI host-side with no container: `config ls` reads and never launches.
func TestConfigTargetResolvesFromTheCwd(t *testing.T) {
	requireJail(t)

	dir := writeProjectWithPacks(t, `{"network": {"mode": "bridge"}}`, "claude")

	inside := runYoloCLI(t, dir, "config", "ls")
	if inside.rc != 0 {
		t.Fatalf("config ls in a workspace rc=%d\n%s\n%s", inside.rc, inside.stdout, inside.stderr)
	}
	if !strings.Contains(inside.combined(), "jail notch") || !strings.Contains(inside.combined(), dir) {
		t.Errorf("a workspace cwd must resolve THAT workspace at the jail notch, and the "+
			"disclosure must name it:\n%s", inside.combined())
	}

	// A directory that is neither a workspace nor inside one: no `.yolo/config-boot.json`
	// and no workspace config, which is exactly the marker OQ-CR2 ruled.
	outside := t.TempDir()
	out := runYoloCLI(t, outside, "config", "ls")
	if out.rc != 0 {
		t.Fatalf("a non-workspace cwd must ANSWER about the host, not refuse: rc=%d\n%s\n%s",
			out.rc, out.stdout, out.stderr)
	}
	if !strings.Contains(out.combined(), "host notch") {
		t.Errorf("a non-workspace cwd must resolve the host target and say so:\n%s", out.combined())
	}
	if strings.Contains(out.combined(), outside) {
		t.Errorf("the host target must not be described as a workspace at the cwd — that is "+
			"the two-predicate defect the design removed:\n%s", out.combined())
	}
}
