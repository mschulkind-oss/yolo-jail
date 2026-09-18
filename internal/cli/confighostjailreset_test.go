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

// runningJailStdout is what a podman probe prints for a live jail launched from ws — the
// frozen naming contract, resolved rather than spelled, so this fixture cannot claim a
// container name a launch would not use.
func runningJailStdout(ws string) string {
	return runtime.FromWorkspace(ws) + " running\n"
}

// A STOPPED JAIL'S CAPTURES ARE DISCARDABLE FROM THE HOST, with no --force. Both halves run:
// the workspace store's sidecars go, and the surface file in the jail's own home overlay is
// truncated — deleting only the sidecars would leave the next boot's adoption to put the
// edits straight back.
func TestHostSideResetDiscardsAStoppedJailsCaptures(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	withJailLiveness(t, "", true) // the runtime answered: nothing is running
	ws, store := withWorkspaceCwd(t)
	writeSidecar(t, store, "claude", "settings", `{"theme":"the agents edit"}`, `{"theme":"yolos"}`)
	// The jail's own copy of the file, at the host-side path that backs /home/agent/.claude
	// — the same inode the jail writes, which is what makes this reachable at all.
	surface := filepath.Join(ws, ".yolo", "home", "claude", "settings.json")
	writeFile(t, surface, `{"theme":"the agents edit"}`)

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"reset", "claude/settings"}, &out, &errw); rc != 0 {
		t.Fatalf("host-side reset of a stopped jail: rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if _, err := os.Stat(filepath.Join(store, "claude-settings.overlay.json")); !os.IsNotExist(err) {
		t.Errorf("the capture overlay survived (err=%v) — the next launch would re-apply the "+
			"very edit the user just discarded", err)
	}
	data, err := os.ReadFile(surface)
	if err != nil {
		t.Fatalf("read the jail's surface after reset: %v", err)
	}
	if strings.Contains(string(data), "the agents edit") {
		t.Errorf("reset left the edit in the jail's own file. Deleting the sidecars alone is "+
			"NOT the discard: the next boot finds no baseline, takes the first-migration "+
			"branch and ADOPTS this file, so the edit comes back:\n%s", data)
	}
	// AND IT DID NOT TOUCH THE INVOKING HUMAN'S HOME, which is the hazard the guard this
	// replaces was really about. expandHome is the real home here.
	if _, err := os.Stat(expandHome("~/.claude/settings.json")); !os.IsNotExist(err) {
		t.Errorf("the reset wrote the invoking user's own home (stat err=%v) — a workspace "+
			"target must resolve the workspace's home overlay and nothing else", err)
	}
}

// A RUNNING JAIL REFUSES, and the refusal names the command that works. The file is live:
// the agent inside may read it at any moment, and the jail captures on TERMINATE, which
// would fold this truncation back in as an edit — the discard undoing itself.
func TestHostSideResetRefusesWhileThatWorkspacesJailIsRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	ws, store := withWorkspaceCwd(t)
	withJailLiveness(t, runningJailStdout(ws), true)
	writeSidecar(t, store, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"reset", "claude/settings"}, &out, &errw); rc == 0 {
		t.Fatalf("reset wrote a RUNNING jail's surfaces:\n%s%s", out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "refusing") ||
		!strings.Contains(errw.String(), "INSIDE that jail") {
		t.Errorf("the refusal does not name the in-jail command, which is available exactly "+
			"now and is the whole reason refusing costs nothing:\n%s", errw.String())
	}
	if _, err := os.Stat(filepath.Join(store, "claude-settings.overlay.json")); err != nil {
		t.Errorf("a refused reset touched the store anyway (err=%v)", err)
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
func TestForceReachesTheRunningJailRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stdout string
		ok     bool
	}{
		{"a running jail", "", true},
		{"an unqueryable runtime", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("YOLO_VERSION", "")
			ws, store := withWorkspaceCwd(t)
			stdout := tc.stdout
			if tc.ok {
				stdout = runningJailStdout(ws)
			}
			withJailLiveness(t, stdout, tc.ok)
			writeSidecar(t, store, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

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

// THE PROBE ASKS ABOUT THE TARGET'S WORKSPACE, not about any live jail. Another workspace's
// jail is running here, and this one's is not.
func TestTheRefusalIsKeyedOnThisWorkspacesJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	withJailLiveness(t, runningJailStdout(t.TempDir())+"yolo-unrelated-00000000 running\n", true)
	_, store := withWorkspaceCwd(t)
	writeSidecar(t, store, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

	var out, errw bytes.Buffer
	if rc := configRunW([]string{"reset", "claude/settings"}, &out, &errw); rc != 0 {
		t.Fatalf("a DIFFERENT workspace's running jail blocked this one's reset: rc=%d\n%s%s",
			rc, out.String(), errw.String())
	}
}

// A `user` SURFACE IS TRUNCATED FOR REAL, which is the residue this step made reachable.
// Its codec, defaults and managed layers come from the `host_files` entry whose Slug
// matches; without them the surface had no codec at all and reset discarded the sidecars and
// left the file — an undo that undid half of itself.
func TestHostSideResetTruncatesAUserSurfaceFromItsDeclaredEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	withJailLiveness(t, "", true)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"host_files":[{"path":".config/mytool/config.json","mode":"capture",`+
			`"defaults":{"level":"info"}}]}`)
	ws, store := withWorkspaceCwd(t)

	// The entry's own escaping, not a hand-spelled guess: config.HostFileEntry.Slug passes
	// `.`, `-` and alphanumerics through and percent-escapes the rest.
	slug := config.HostFileEntry{Path: ".config/mytool/config.json"}.Slug()
	if got := userSurfaceFor(slug); got.Codec == "" {
		t.Fatalf("the declared entry gave the surface no codec (%+v) — the slug fixture no "+
			"longer matches config.HostFileEntry.Slug, so this test is measuring nothing", got)
	}
	writeSidecar(t, store, "user", slug, `{"level":"debug"}`, `{"level":"info"}`)
	surface := filepath.Join(ws, ".yolo", "home", "config", "mytool", "config.json")
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
		t.Errorf("reset discarded the sidecars and left the edit in the file — half an undo:\n%s",
			data)
	}
	if !strings.Contains(string(data), "info") {
		t.Errorf("the truncation did not compose the entry's declared `defaults` layer, so "+
			"reset emptied a file instead of returning it to its pure render:\n%s", data)
	}
}
