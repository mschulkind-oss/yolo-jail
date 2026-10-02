package hostfloor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
)

// confine_test.go pins that the floor's materialize — which runs on the HOST — places nothing a
// capture's manifest names outside the floor's own install directory. The manifest is written by
// the capture driver inside the capture jail, so the floor treats it as the jail's claim.

// TestTheFloorRefusesACaptureWhoseManifestWritesOutsideIt: a capture that holds a runnable claude
// AND an entry pointing outside the floor — by `..`, or beneath a link the same materialize makes,
// or outside the capture surfaces — installs nothing: no launcher, and nothing where the entry
// pointed.
func TestTheFloorRefusesACaptureWhoseManifestWritesOutsideIt(t *testing.T) {
	outside := resolvedTemp(t)
	climb := func(abs string) string {
		return ".local/" + strings.Repeat("../", 64) + strings.TrimPrefix(abs, "/")
	}
	cases := []struct {
		name    string
		shape   func(tree string, m *capture.Manifest)
		escaped string
		want    string
	}{
		{name: "a path that climbs with ..", escaped: filepath.Join(outside, "by-dotdot"),
			want: "climbs out of the home",
			shape: func(_ string, m *capture.Manifest) {
				m.Entries = append(m.Entries, capture.ManifestEntry{Path: climb(filepath.Join(outside, "by-dotdot")),
					Kind: capture.KindDir, Mode: "0755"})
			}},
		{name: "a path beneath a link the same run makes", escaped: filepath.Join(outside, "by-link"),
			want: "write through that symlink",
			shape: func(tree string, m *capture.Manifest) {
				must(t, os.Symlink(outside, filepath.Join(tree, ".local", "evil")))
				m.Entries = append(m.Entries,
					capture.ManifestEntry{Path: ".local/evil", Kind: capture.KindSymlink, Target: outside},
					capture.ManifestEntry{Path: ".local/evil/by-link", Kind: capture.KindDir, Mode: "0755"})
			}},
		{name: "a path outside the capture surfaces", want: "outside the capture surfaces",
			shape: func(tree string, m *capture.Manifest) {
				must(t, os.MkdirAll(filepath.Join(tree, ".ssh"), 0o755))
				must(t, os.WriteFile(filepath.Join(tree, ".ssh", "authorized_keys"), []byte("k"), 0o644))
				m.Entries = append(m.Entries,
					capture.ManifestEntry{Path: ".ssh", Kind: capture.KindDir, Mode: "0755"},
					capture.ManifestEntry{Path: ".ssh/authorized_keys", Kind: capture.KindFile, Mode: "0644", Size: 1})
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newLinuxWorld(t)
			cs := newCaptureStore(t)
			cs.addShaped("claude", "2.1.267", true, c.shape)
			w.floor.ResolveCapture = cs.resolve
			_, _, err := w.floor.Ensure(context.Background(), installerProgram("claude", "claude"))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Ensure: err %v, want a refusal naming %q\n%s", err, c.want, w.out.String())
			}
			if c.escaped != "" {
				if _, serr := os.Lstat(c.escaped); serr == nil {
					t.Errorf("%s was created outside the floor", c.escaped)
				}
			}
			if _, serr := os.Lstat(w.floor.Launcher("claude")); serr == nil {
				t.Error("a launcher was written for a refused capture")
			}
			if dirs, _ := os.ReadDir(w.floor.programsDir("claude")); len(dirs) != 0 {
				t.Errorf("the refused install left %d install directories", len(dirs))
			}
		})
	}
}
