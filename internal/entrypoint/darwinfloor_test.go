package entrypoint

// darwinfloor_test.go pins the three generators that CHANGE THEIR ANSWER the
// moment the non-container floor reaches $YOLO_DARWIN_LOGIN_PATH
// (docs/design/macos-user-provisioning.md, half one).
//
// None of them needed a code change for it, and that is exactly why they need a
// test. All three already ask entrypoint.agentPath "will the agent have this
// binary?", so widening that PATH from "the system dirs plus whatever the user
// declared" to "the system dirs plus 27 packages" silently flips three decisions
// at once:
//
//	GenerateShims          `grep` and `find` become BLOCKED, because their
//	                       declared replacements (rg, fd) now exist. Until the
//	                       floor they did not, so the guardrails pack's rules
//	                       were generated on Linux and silently dropped on a Mac.
//	launchercollision      a pack's `program git` stops getting a launcher,
//	                       because the environment now provides git.
//	AssertRequiredBins     a pack's `requires: rg` stops warning.
//
// Each cell below asserts the flip in BOTH directions — with the floor and
// without it — because a one-sided cell passes against a generator that has
// stopped consulting the PATH at all.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// floorPath writes a fake floor profile bin dir holding `bins` and returns a
// $YOLO_DARWIN_LOGIN_PATH shaped the way macosuser.SandboxPath builds it: the
// per-home prefixes, then the store, then the system dirs.
//
// The SHAPE is not decoration. imageProbePath filters this string by "is it under
// the jail home", so a bare store dir would exercise a path the launcher never
// emits and would not notice a filter that dropped the store along with the
// prefixes.
//
// ⚠ THE SYSTEM DIRS ARE FAKE, AND THEY HAVE TO BE. This suite runs inside the
// yolo-jail image, where /bin/rg and /bin/fd exist — so a cell asserting "without
// the floor there is no rg" would pass on a Mac and fail here, for a reason that
// has nothing to do with the code under test. An empty stand-in models what
// macOS's own /usr/bin actually offers an agent: grep and find, never ripgrep or
// fd. The one thing the real dirs would add is coverage of the system half of the
// PATH, which no cell in this file is about.
func floorPath(t *testing.T, home string, bins ...string) string {
	t.Helper()
	base := t.TempDir()
	storeBin := filepath.Join(base, "yolo-noncontainer-profile", "bin")
	sysBin := filepath.Join(base, "fake-usr-bin")
	for _, d := range []string{storeBin, sysBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range bins {
		if err := os.WriteFile(filepath.Join(storeBin, b), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return strings.Join([]string{
		home + "/.yolo/bin/block",
		home + "/.yolo/bin/launch",
		home + "/.local/bin",
		home + "/.npm-global/bin",
		home + "/.yolo/mise/shims",
		home + "/go/bin",
		storeBin,
		sysBin,
	}, ":")
}

// guardrailsBlockConfig is the shipped `guardrails` pack's two rules, in the wire
// form the launcher bakes onto the bootstrap argv. Spelled out rather than loaded
// so this file states what it is asserting about.
const guardrailsBlockConfig = `[` +
	`{"name":"grep","message":"use rg","suggestion":"rg","replacement":"rg","block_flags":["-r"]},` +
	`{"name":"find","message":"use fd","suggestion":"fd","replacement":"fd"}` +
	`]`

// TestTheFloorIsWhatMakesTheGuardrailsBlockersGeneratable.
//
// A blocker declaring a `replacement` is generated ONLY when that binary is on the
// agent's PATH — the 2026-09-04 fix for a real Mac launch where `grep -r` exited
// 127 pointing at an `rg` that did not exist. macos-user bakes no image, so before
// the floor the rule could only fire if the user happened to declare ripgrep in
// `packages:`: the guardrails pack was, in practice, inert on this backend.
func TestTheFloorIsWhatMakesTheGuardrailsBlockersGeneratable(t *testing.T) {
	t.Run("with the floor, both rules are generated", func(t *testing.T) {
		home := t.TempDir()
		var warnings strings.Builder
		e := NewEnv(map[string]string{
			"JAIL_HOME":              home,
			"YOLO_DARWIN_LOGIN_PATH": floorPath(t, home, "rg", "fd"),
			"YOLO_BLOCK_CONFIG":      guardrailsBlockConfig,
		})
		e.Stderr = &warnings
		if err := GenerateShims(e); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"grep", "find"} {
			if _, err := os.Stat(filepath.Join(e.BlockDir(), name)); err != nil {
				t.Errorf("no %s blocker was generated even though the floor supplies its "+
					"replacement (%v)\nwarnings:\n%s", name, err, warnings.String())
			}
		}
	})

	// The same config against a floorless PATH — what this backend actually had
	// until half one landed.
	t.Run("without the floor, neither is", func(t *testing.T) {
		home := t.TempDir()
		var warnings strings.Builder
		e := NewEnv(map[string]string{
			"JAIL_HOME":              home,
			"YOLO_DARWIN_LOGIN_PATH": floorPath(t, home), // an empty store
			"YOLO_BLOCK_CONFIG":      guardrailsBlockConfig,
		})
		e.Stderr = &warnings
		if err := GenerateShims(e); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"grep", "find"} {
			if _, err := os.Stat(filepath.Join(e.BlockDir(), name)); err == nil {
				t.Errorf("a %s blocker was generated with no replacement on PATH — the "+
					"agent loses the tool and is sent to a binary that is not there", name)
			}
		}
		if !strings.Contains(warnings.String(), "rg") || !strings.Contains(warnings.String(), "fd") {
			t.Errorf("the skip did not name the missing replacements:\n%s", warnings.String())
		}
	})
}

// TestTheFloorCountsAsWhatTheEnvironmentProvides — the launcher-collision arm.
//
// `git` is on the floor, so after half one a pack declaring `program git` must NOT
// get a lazy npm launcher: the launch dir sits ahead of every install prefix, so
// that launcher would stand in front of the real git for every invocation.
//
// The second name is the control. Without it the cell would also pass against a
// generator that wrote no launchers at all — which is the other way to make
// shadowing impossible and the wrong way (B2's own instruction).
func TestTheFloorCountsAsWhatTheEnvironmentProvides(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":              home,
		"YOLO_DARWIN_LOGIN_PATH": floorPath(t, home, "git", "node"),
		"YOLO_PACK_ROOT":         writePackWithProgram(t, "floorshadow", "git", "yolo-not-on-the-floor"),
	})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "git")); !os.IsNotExist(err) {
		t.Errorf("a launcher was written for `git`, which the floor provides (err=%v) — "+
			"with the launch dir ahead of the install prefixes it would mediate every "+
			"git invocation in the sandbox", err)
	}
	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "yolo-not-on-the-floor")); err != nil {
		t.Errorf("a name the floor does NOT provide must still get its launcher, or the "+
			"check has switched the mechanism off: %v", err)
	}
}

// TestImageProbePathKeepsTheFloorAndDropsThePrefixes states the same rule
// directly, which the cell above states through behaviour. Two readings, because
// the rule is the feature: the floor's store dir is "what the launch provides"
// and must survive the per-home filter, while every prefix a launcher INSTALLS
// INTO must not — a probe that kept those stops writing launchers after their own
// first success, which is evergreen working exactly once.
func TestImageProbePathKeepsTheFloorAndDropsThePrefixes(t *testing.T) {
	home := "/Users/_yolojail"
	loginPath := floorPath(t, home, "git")
	storeBin := ""
	for _, d := range strings.Split(loginPath, ":") {
		if strings.HasSuffix(d, "yolo-noncontainer-profile/bin") {
			storeBin = d
		}
	}
	if storeBin == "" {
		t.Fatalf("fixture did not produce a store bin dir: %q", loginPath)
	}
	e := NewEnv(map[string]string{
		"JAIL_HOME":              home,
		"YOLO_DARWIN_LOGIN_PATH": loginPath,
	})
	got := imageProbePath(e)
	if !strings.Contains(got, storeBin) {
		t.Errorf("imageProbePath dropped the floor's store dir:\n  got %q\n  want it to contain %q",
			got, storeBin)
	}
	for _, prefix := range []string{home + "/.local/bin", home + "/.npm-global/bin", home + "/go/bin"} {
		if strings.Contains(got, prefix) {
			t.Errorf("imageProbePath kept the install prefix %q — after one successful "+
				"install the launcher stops being written and the update arm it carries "+
				"never runs again", prefix)
		}
	}
}

// TestTheFloorSatisfiesAPacksRequires — the third arm. A `requires` entry is a
// warning, not a refusal, so this one fails SILENTLY in the direction that matters
// least and is therefore the easiest of the three to leave broken.
func TestTheFloorSatisfiesAPacksRequires(t *testing.T) {
	home := t.TempDir()
	packRoot := writeRequiresPack(t, "", "rg")

	var withFloor strings.Builder
	e := NewEnv(map[string]string{
		"JAIL_HOME":              home,
		"YOLO_PACK_ROOT":         packRoot,
		"YOLO_DARWIN_LOGIN_PATH": floorPath(t, home, "rg"),
	})
	e.Stderr = &withFloor
	AssertRequiredBins(e)
	if strings.Contains(withFloor.String(), "requires rg") {
		t.Errorf("warned that `rg` is missing while the floor supplies it:\n%s", withFloor.String())
	}

	var withoutFloor strings.Builder
	e2 := NewEnv(map[string]string{
		"JAIL_HOME":              home,
		"YOLO_PACK_ROOT":         packRoot,
		"YOLO_DARWIN_LOGIN_PATH": floorPath(t, home),
	})
	e2.Stderr = &withoutFloor
	AssertRequiredBins(e2)
	if !strings.Contains(withoutFloor.String(), "rg") {
		t.Errorf("a genuinely absent `requires` binary went unreported — the cell above "+
			"would then pass against a probe that warns about nothing:\n%s", withoutFloor.String())
	}
}
