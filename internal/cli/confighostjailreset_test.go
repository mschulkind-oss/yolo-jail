package cli

// confighostjailreset_test.go pins the FOURTH DISPOSITION
// ([OQ-CR4](docs/reference/config-target-resolution.md#oq-cr4), ruled (a) with the running-jail
// refusal): host-side, `yolo config reset` discards a JAIL's captured edits.
//
// The matrix had three cells, so the only shipped exit from a captured edit was the verb
// inside the owning jail — and discarding a STOPPED jail's captures therefore required
// launching it, which renders and captures first. The undo was reachable only by performing
// the act being undone.
//
// Every test here drives configRunW from a scratch workspace through withWorkspaceCwd. That
// is not hygiene: the resolution walks the cwd, this package's directory sits under the live
// /workspace checkout, and a reset that resolved THAT would delete this session's own
// sidecars.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// withJailLiveness stubs the runtime probe with a fixed answer, and it is not optional: a
// bare runner's answer depends on whether a container runtime happens to be installed, so
// without it two of the three dispositions are unreachable and the third is ambient.
func withJailLiveness(t *testing.T, stdout string, ok bool) {
	t.Helper()
	orig := jailLivenessProbe
	jailLivenessProbe = func([]string) (string, bool) { return stdout, ok }
	t.Cleanup(func() { jailLivenessProbe = orig })
}

// jailRuntimes is the set every runtime-sensitive test below runs over. BOTH, because the
// two arms are different code and one of them was unreachable on Linux and unavoidable on a
// Mac — see withJailRuntime.
var jailRuntimes = []string{"podman", "container"}

// withJailRuntime pins the runtime a workspace target resolves, and it is NOT hygiene.
//
// ⚠ THREE TESTS IN THIS FILE WERE AMBIENT ON THE DEVELOPER'S PATH, and two more passed
// VACUOUSLY because of it. `jailConfigTarget` resolves `detectListingRuntime`, which on macOS
// prefers `container` when Apple's CLI is installed — and the runtime decides two things every
// test here leans on:
//
//   - WHERE the jail home holds a surface. Apple Container binds ws_state AT the home, so
//     `~/.claude/settings.json` keeps its dot; podman nests a per-dir overlay with the dot
//     STRIPPED (jailHomeRel, whose own test pins both arms). A fixture that spells
//     the podman path writes a file production correctly never looks at, so the truncation
//     silently found nothing and `reset` looked like it had left the edit behind.
//   - WHICH PROBE and therefore WHICH PARSER reads the liveness stub. `container ls` is a
//     fixed table and ParseContainerLsLive DROPS THE HEADER ROW, so a header-less stub parsed
//     to the empty set: the running-jail refusal never fired, and
//     TestForceReachesTheRunningJailRefusal passed without exercising --force at all — it got
//     rc=0 from "nothing is running" instead of from the override. That is the shape AGENTS.md
//     names: delete the thing under test and the test still passes.
//
// So the runtime is now an INPUT, spelled per case, and every runtime-sensitive test runs over
// both arms. YOLO_RUNTIME rather than the workspace's `runtime` key because it is the highest
// precedence source detectListingRuntime reads (env > config > platform probe), so an ambient
// YOLO_RUNTIME in the developer's own shell cannot defeat it.
func withJailRuntime(t *testing.T, rt string) {
	t.Helper()
	t.Setenv("YOLO_RUNTIME", rt)
}

// runningJailStdout is what a probe prints for a live jail launched from ws, SHAPED FOR THE
// RUNTIME that will parse it.
//
// The NAME is the frozen naming contract, resolved rather than spelled, so this fixture cannot
// claim a container name a launch would not use. The SHAPE is the other half of the same rule:
// `container ls` emits a fixed table whose first line is a header, and its parser drops that
// line, so a stub without one claims a live jail the production parser cannot see.
func runningJailStdout(rt, ws string) string {
	name := runtime.FromWorkspace(ws)
	if rt == "container" {
		return "ID IMAGE OS ARCH STATE IP CPUS MEMORY STARTED\n" + name + " running\n"
	}
	return name + " running\n"
}

// jailSurfaceFile is where THIS runtime's jail home holds a surface, from the production
// mapping rather than a hand-spelled path.
//
// Resolved, not spelled, for runningJailStdout's reason exactly: the mapping is
// runtime-dependent, it has its own test pinning both arms and the cross negatives
// (configcapture_test.go), and the subject here is what `reset` does to the file — not where
// the file is. Spelling it is what made these tests podman-only.
func jailSurfaceFile(t *testing.T, ws, rt, surfacePath string) string {
	t.Helper()
	rel, ok := jailHomeRel(rt, surfacePath)
	if !ok {
		t.Fatalf("the jail home maps no location for %q on %s, so this test would assert "+
			"about a path production never resolves", surfacePath, rt)
	}
	return filepath.Join(paths.WorkspaceHomeState(ws), rel)
}

// A STOPPED JAIL'S CAPTURES ARE DISCARDABLE FROM THE HOST, with no --force. Both halves run:
// the workspace store's sidecars go, and the surface file in the jail's own home overlay is
// truncated — deleting only the sidecars would leave the next boot's adoption to put the
// edits straight back.
func TestHostSideResetDiscardsAStoppedJailsCaptures(t *testing.T) {
	for _, rt := range jailRuntimes {
		t.Run(rt, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("YOLO_VERSION", "")
			withJailRuntime(t, rt)
			withJailLiveness(t, "", true) // the runtime answered: nothing is running
			ws, store := withWorkspaceCwd(t)
			writeSidecar(t, store, "claude", "settings",
				`{"theme":"the agents edit"}`, `{"theme":"yolos"}`)
			// The jail's own copy of the file, at the host-side path that backs
			// /home/agent/.claude — the same inode the jail writes, which is what makes this
			// reachable at all. WHERE that is depends on the runtime; see jailSurfaceFile.
			surface := jailSurfaceFile(t, ws, rt, "~/.claude/settings.json")
			writeFile(t, surface, `{"theme":"the agents edit"}`)

			var out, errw bytes.Buffer
			if rc := configRunW([]string{"reset", "claude/settings"}, &out, &errw); rc != 0 {
				t.Fatalf("host-side reset of a stopped jail: rc=%d\n%s%s",
					rc, out.String(), errw.String())
			}
			if _, err := os.Stat(filepath.Join(store, "claude-settings.overlay.json")); !os.IsNotExist(err) {
				t.Errorf("the capture overlay survived (err=%v) — the next launch would "+
					"re-apply the very edit the user just discarded", err)
			}
			data, err := os.ReadFile(surface)
			if err != nil {
				t.Fatalf("read the jail's surface after reset: %v", err)
			}
			if strings.Contains(string(data), "the agents edit") {
				t.Errorf("reset left the edit in the jail's own file. Deleting the sidecars "+
					"alone is NOT the discard: the next boot finds no baseline, takes the "+
					"first-migration branch and ADOPTS this file, so the edit comes back:\n%s", data)
			}
			// AND IT DID NOT TOUCH THE INVOKING HUMAN'S HOME, which is the hazard the guard
			// this replaces was really about. expandHome is the real home here.
			if _, err := os.Stat(expandHome("~/.claude/settings.json")); !os.IsNotExist(err) {
				t.Errorf("the reset wrote the invoking user's own home (stat err=%v) — a "+
					"workspace target must resolve the workspace's home overlay and nothing "+
					"else", err)
			}
		})
	}
}

// A RUNNING JAIL REFUSES, and the refusal names the command that works. The file is live:
// the agent inside may read it at any moment, and the jail captures on TERMINATE, which
// would fold this truncation back in as an edit — the discard undoing itself.
func TestHostSideResetRefusesWhileThatWorkspacesJailIsRunning(t *testing.T) {
	for _, rt := range jailRuntimes {
		t.Run(rt, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("YOLO_VERSION", "")
			withJailRuntime(t, rt)
			ws, store := withWorkspaceCwd(t)
			withJailLiveness(t, runningJailStdout(rt, ws), true)
			writeSidecar(t, store, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

			var out, errw bytes.Buffer
			if rc := configRunW([]string{"reset", "claude/settings"}, &out, &errw); rc == 0 {
				t.Fatalf("reset wrote a RUNNING jail's surfaces:\n%s%s",
					out.String(), errw.String())
			}
			if !strings.Contains(errw.String(), "refusing") ||
				!strings.Contains(errw.String(), "INSIDE that jail") {
				t.Errorf("the refusal does not name the in-jail command, which is available "+
					"exactly now and is the whole reason refusing costs nothing:\n%s", errw.String())
			}
			if _, err := os.Stat(filepath.Join(store, "claude-settings.overlay.json")); err != nil {
				t.Errorf("a refused reset touched the store anyway (err=%v)", err)
			}
		})
	}
}

// "COULD NOT ASK" IS NOT "NOTHING IS RUNNING". The probe is tri-state and the third answer
// refuses too — this repo's standing rule for a sweeper that cannot ask, applied to the case
// it is strongest for (a write).
func TestHostSideResetRefusesWhenTheRuntimeCannotBeAsked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	withJailLiveness(t, "", false) // spawn error / non-zero exit
	_, store := withWorkspaceCwd(t)
	writeSidecar(t, store, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"reset", "claude/settings"}, &out, &errw); rc == 0 {
		t.Fatalf("an unqueryable runtime was read as an idle machine:\n%s%s",
			out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "could not ask") {
		t.Errorf("the refusal does not say the probe failed, so it reads as a claim about "+
			"the jail rather than about the runtime:\n%s", errw.String())
	}
	if _, err := os.Stat(filepath.Join(store, "claude-settings.overlay.json")); err != nil {
		t.Errorf("a refused reset touched the store anyway (err=%v)", err)
	}
}

// --force REACHES BOTH REFUSALS, which is the plan's Blocker 4 answered rather than left
// implicit. The flag's shipped meaning is "I really mean to write these files"; narrowing it
// here would retract a hatch this CLI has always had for exactly this write, and the
// unqueryable case in particular needs it, because the remedy the running refusal offers may
// be unreachable precisely when the runtime is broken.
// ⚠ THE RUNNING-JAIL CASE PASSED VACUOUSLY ON A MAC until the runtime became an input. With
// Apple Container resolved and a header-less liveness stub, ParseContainerLsLive saw an empty
// set, so this got its rc=0 from "nothing is running" rather than from --force overriding
// anything — the test would have survived deleting the override it exists to pin. Both arms run
// now, and runningJailStdout shapes the stub for the parser that will read it.
func TestForceReachesTheRunningJailRefusal(t *testing.T) {
	for _, rt := range jailRuntimes {
		for _, tc := range []struct {
			name string
			ok   bool
		}{
			{"a running jail", true},
			{"an unqueryable runtime", false},
		} {
			t.Run(rt+"/"+tc.name, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				t.Setenv("YOLO_VERSION", "")
				withJailRuntime(t, rt)
				ws, store := withWorkspaceCwd(t)
				stdout := ""
				if tc.ok {
					stdout = runningJailStdout(rt, ws)
				}
				withJailLiveness(t, stdout, tc.ok)
				writeSidecar(t, store, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

				// THE REFUSAL IS REALLY IN THE PATH, asserted rather than assumed: without
				// this the running arm cannot tell "--force overrode the refusal" from "there
				// was no refusal to override", which is exactly how it used to pass.
				if tc.ok {
					if state, _ := workspaceJailLiveness(jailConfigTarget(ws, "the test")); state != jailRunning {
						t.Fatalf("the fixture does not read as a running jail on %s (state=%v), "+
							"so --force has nothing to reach and this case asserts nothing", rt, state)
					}
				}

				var out, errw bytes.Buffer
				if rc := configRunW([]string{"reset", "claude/settings", "--force"}, &out, &errw); rc != 0 {
					t.Fatalf("--force did not reach the refusal: rc=%d\n%s%s", rc,
						out.String(), errw.String())
				}
				if _, err := os.Stat(filepath.Join(store, "claude-settings.overlay.json")); !os.IsNotExist(err) {
					t.Errorf("--force left the overlay in place (err=%v)", err)
				}
			})
		}
	}
}

// THE PROBE ASKS ABOUT THE TARGET'S WORKSPACE, not about any live jail. Another workspace's
// jail is running here, and this one's is not.
//
// ⚠ THE OTHER WORKSPACE'S ROW WAS BEING EATEN AS A HEADER on a Mac: runningJailStdout emitted
// no header, ParseContainerLsLive dropped its first line, and the row this test is ABOUT
// vanished before the discrimination was reached. It passed on the remaining row, for a reason
// that had nothing to do with keying.
func TestTheRefusalIsKeyedOnThisWorkspacesJail(t *testing.T) {
	for _, rt := range jailRuntimes {
		t.Run(rt, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("YOLO_VERSION", "")
			withJailRuntime(t, rt)
			other := runningJailStdout(rt, t.TempDir())
			withJailLiveness(t, other+"yolo-unrelated-00000000 running\n", true)
			_, store := withWorkspaceCwd(t)
			writeSidecar(t, store, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

			var out, errw bytes.Buffer
			if rc := configRunW([]string{"reset", "claude/settings"}, &out, &errw); rc != 0 {
				t.Fatalf("a DIFFERENT workspace's running jail blocked this one's reset: "+
					"rc=%d\n%s%s", rc, out.String(), errw.String())
			}
		})
	}
}

// A `user` SURFACE IS TRUNCATED FOR REAL, which is the residue this step made reachable.
// Its codec, defaults and managed layers come from the `host_files` entry whose Slug
// matches; without them the surface had no codec at all and reset discarded the sidecars and
// left the file — an undo that undid half of itself.
func TestHostSideResetTruncatesAUserSurfaceFromItsDeclaredEntry(t *testing.T) {
	for _, rt := range jailRuntimes {
		t.Run(rt, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_VERSION", "")
			withJailRuntime(t, rt)
			withJailLiveness(t, "", true)
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
				`{"host_files":[{"path":".config/mytool/config.json","mode":"capture",`+
					`"defaults":{"level":"info"}}]}`)
			ws, store := withWorkspaceCwd(t)

			// The entry's own escaping, not a hand-spelled guess: config.HostFileEntry.Slug
			// passes `.`, `-` and alphanumerics through and percent-escapes the rest.
			slug := config.HostFileEntry{Path: ".config/mytool/config.json"}.Slug()
			if got := userSurfaceFor(slug); got.Codec == "" {
				t.Fatalf("the declared entry gave the surface no codec (%+v) — the slug "+
					"fixture no longer matches config.HostFileEntry.Slug, so this test is "+
					"measuring nothing", got)
			}
			writeSidecar(t, store, "user", slug, `{"level":"debug"}`, `{"level":"info"}`)
			surface := jailSurfaceFile(t, ws, rt, "~/.config/mytool/config.json")
			writeFile(t, surface, `{"level":"debug"}`)

			var out, errw bytes.Buffer
			if rc := configRunW([]string{"reset", "user"}, &out, &errw); rc != 0 {
				t.Fatalf("reset user: rc=%d\n%s%s", rc, out.String(), errw.String())
			}
			data, err := os.ReadFile(surface)
			if err != nil {
				t.Fatalf("read the user surface after reset: %v", err)
			}
			if strings.Contains(string(data), "debug") {
				t.Errorf("reset discarded the sidecars and left the edit in the file — half "+
					"an undo:\n%s", data)
			}
			if !strings.Contains(string(data), "info") {
				t.Errorf("the truncation did not compose the entry's declared `defaults` "+
					"layer, so reset emptied a file instead of returning it to its pure "+
					"render:\n%s", data)
			}
		})
	}
}
