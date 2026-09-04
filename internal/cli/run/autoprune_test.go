package run

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// autoprune_test.go pins the delivery of `programs.autoprune` (OQ-PD4's third clause,
// program-delivery.md §10 step four) from the user's config to the jail's environment.
//
// It is the CALL-SITE half of the option. internal/config pins the reader and
// internal/entrypoint pins what the jail does with the variable, and both of those stay green
// with this launcher emitting nothing at all — which would be an option that is permanently,
// silently off.

// writeUserAutoprune lays down a user config carrying the option.
func writeUserAutoprune(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// OFF: the argv is byte-identical to the frozen golden. The option must be a pure no-op for
// everyone who has not asked for it — the same claim
// TestAssembleWritableHomeDirsNoneMatchesGolden makes, and for a stronger reason: this one
// deletes files.
func TestAutopruneEmitsNothingByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	got := o.assembleRunCmd(relocationInput(t, "podman", "/ws/.yolo/home", nil))
	for _, a := range got {
		if a == "YOLO_PROGRAMS_AUTOPRUNE=1" {
			t.Fatal("a launch with no `programs` key asked the jail to DELETE its orphans")
		}
	}
	if !slices.Equal(got, podmanLinuxGolden(home)) {
		t.Errorf("argv drifted from the golden with autoprune unset:\ngot:  %v\nwant: %v",
			got, podmanLinuxGolden(home))
	}
}

// ON: the variable is emitted, once, with the value the entrypoint reads.
//
// MUTATION: delete the `config.ProgramsAutoprune(nil)` block from assembleRunCmd and this
// goes red while every other autoprune test in the repo stays green.
func TestAutopruneIsDeliveredWhenTheUserAsksForIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeUserAutoprune(t, home, `{"programs": {"autoprune": true}}`)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)

	got := o.assembleRunCmd(relocationInput(t, "podman", "/ws/.yolo/home", nil))
	n := 0
	for _, a := range got {
		if a == "YOLO_PROGRAMS_AUTOPRUNE=1" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("YOLO_PROGRAMS_AUTOPRUNE=1 appears %d times, want exactly 1:\n%v", n, got)
	}
}

// A WORKSPACE config cannot deliver it. The loader reads user scope directly, so the launcher
// carries that property to the container without a second check — and this is where the
// property is worth pinning, because this is the process that would actually emit the
// variable for a repo that asked for it.
func TestAutopruneIsNotDeliverableByAWorkspaceConfig(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"programs": {"autoprune": true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The workspace IS the cwd (`yolo config-ref`), so this is what makes the assertion
	// bite: a delivery that consulted the merged config would find the key right here.
	t.Chdir(ws)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)

	got := o.assembleRunCmd(relocationInput(t, "podman", ws+"/.yolo/home", nil))
	for _, a := range got {
		if a == "YOLO_PROGRAMS_AUTOPRUNE=1" {
			t.Fatal("A REPO-COMMITTED WORKSPACE CONFIG turned on autoprune for this launch " +
				"— a file that travels with the repo and is agent-editable must not be " +
				"able to authorise deleting the user's programs")
		}
	}
}
