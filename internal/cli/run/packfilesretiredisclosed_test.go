package run

// A pack-file mountpoint retired at launch is SAID (requests #25). The retire itself already
// existed (retirePackFileMountpoints, from the ownership record beside the overlay); it removed
// silently, and a launch has no quiet mode (AGENTS.md, OQ-RO3). On Apple Container the removed
// path is a delivered COPY of the pack's file, so a silent removal was a file leaving the jail
// home unannounced.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestTheLaunchNamesEachPackFileItRetires(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			ws := t.TempDir()
			p := workspaceFilesPack(t, "pi-extension", "extension.ts",
				".pi/agent/extensions/thinking-preview.ts", ".pi")
			rel := filepath.Join("pi", "agent", "extensions", "thinking-preview.ts")
			if rt == "container" {
				rel = filepath.Join(".pi", "agent", "extensions", "thinking-preview.ts")
			}

			o := goldenOptions(ws, home)
			o.Stderr = &bytes.Buffer{}
			wsState := o.prepareWsState(nil, []*packload.Pack{p}, rt)
			if rt == "container" {
				acMaterialize(filepath.Join(p.Root, "extension.ts"), rel, wsState)
			}
			if _, err := os.Lstat(filepath.Join(wsState, rel)); err != nil {
				t.Fatalf("the pack's file was never provisioned at %s: %v", rel, err)
			}

			var stderr bytes.Buffer
			o.Stderr = &stderr
			o.prepareWsState(nil, nil, rt)
			if _, err := os.Lstat(filepath.Join(wsState, rel)); !os.IsNotExist(err) {
				t.Fatalf("the dropped contribution's file was not retired: %v", err)
			}
			if got := stderr.String(); !strings.Contains(got, "thinking-preview.ts") || !strings.Contains(got, "no longer ships") {
				t.Errorf("the retire was silent; stderr:\n%s", got)
			}
		})
	}
}
