package run

// contextdir_test.go pins $YOLO_CONTEXT_DIR on the container backends
// (docs/design/context-mounts.md §4 step 2, CX-D4). The podman half is the golden argv
// (TestAssembleRunCmdPodmanLinuxGolden); this is the Apple Container half, through the same
// assembler, so deleting the one `-e` emission fails both.

import (
	"slices"
	"testing"
)

// The variable is on the Apple Container argv, with no context mount declared at all, and it
// is /ctx — not ~/.yolo-ctx, which is YOLO_CTX_ROOT's (the entrypoint's composition input).
func TestAppleContainerArgvCarriesTheContextDir(t *testing.T) {
	argv, _ := assembleWithMounts(t, "container", []any{})
	if !slices.Contains(argv, "YOLO_CONTEXT_DIR=/ctx") {
		t.Errorf("the Apple Container argv does not carry YOLO_CONTEXT_DIR=/ctx:\n%v", argv)
	}
	for _, a := range argv {
		if a == "YOLO_CONTEXT_DIR=/home/agent/.yolo-ctx" {
			t.Errorf("the context dir names the composition-input root: %q", a)
		}
	}
}
