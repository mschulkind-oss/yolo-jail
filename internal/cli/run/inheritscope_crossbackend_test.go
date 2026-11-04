package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// AN APPLE CONTAINER HOME IN A LATER macos-user LAUNCH'S WAY (the Mac run of 2026-11-03, §5; MMP-D32).
// Since slice 12a both backends use <ws>/.yolo/home as HOME with the same dotted layout, and an
// Apple Container launch COPIES the generated user config into it (writable, yolo's generated
// header first), where macos-user delivers a sealed 0444 file it can prove. Nothing proves the
// copy unedited, so it stays a refusal; but the refusal names the cause — this workspace's home
// was used by Apple Container — and the step that clears every such residue at once, keeping it:
// move the whole home aside. The launch is refused on the host, before the sandbox, and the file
// is left as it was. Red while the refusal was the generic "review it" line.
func TestMacosUserRefusesAnAppleContainerHomeNamingTheBackendAndTheMove(t *testing.T) {
	for _, mode := range []macosuser.Mode{macosuser.ModeAccount, macosuser.ModeSelf} {
		t.Run(mode.String(), func(t *testing.T) {
			treeLaunchHome(t, true)
			f := newInheritedRelaunchFixture(t, mode)
			if err := os.MkdirAll(filepath.Dir(f.delivered), 0o755); err != nil {
				t.Fatal(err)
			}
			body := []byte(config.InheritHeader(config.InheritPreflight, "2026-11-03T10:00:00Z") + "{}\n")
			if err := os.WriteFile(f.delivered, body, 0o644); err != nil {
				t.Fatal(err)
			}
			rc, stderr, installErr := f.launch()
			if rc == 0 || f.dispatched != 0 {
				t.Fatalf("a launch over Apple Container's copy = %d, dispatched %d (want a host refusal "+
					"before dispatch), installer error %v\nstderr:\n%s", rc, f.dispatched, installErr, stderr)
			}
			flat := strings.Join(strings.Fields(stderr), " ")
			for _, want := range []string{
				f.delivered,
				"Apple Container",
				shquote.JoinDisplay([]string{"mv", f.sidecar, f.sidecar + ".apple-container"}),
				"launch with `--setup apple-container-self`",
			} {
				if !strings.Contains(flat, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, stderr)
				}
			}
			if got, err := os.ReadFile(f.delivered); err != nil || !bytes.Equal(got, body) {
				t.Fatalf("the refusal changed Apple Container's file: read error %v", err)
			}
		})
	}
}

// A file in the way that is NOT Apple Container's shape keeps the generic refusal: a sealed
// (0444) file is macos-user's own past delivery, and one without yolo's generated header is the
// owner's.
func TestTheAppleContainerRefusalNeedsItsShape(t *testing.T) {
	dir := t.TempDir()
	uid := os.Geteuid()
	write := func(name string, body []byte, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, body, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	generated := []byte(config.InheritTOMLHeader(config.InheritPreflight, "") + "\n")
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"Apple Container's TOML copy", write("a.toml", generated, 0o644), true},
		{"Apple Container's JSONC copy", write("a.jsonc", []byte(config.InheritHeader(config.InheritPreflight, "")+"{}\n"), 0o644), true},
		{"a sealed delivery", write("sealed.toml", generated, 0o444), false},
		{"the owner's own file", write("own.toml", []byte("[packs]\n"), 0o644), false},
	} {
		if got := isAppleContainerInheritedCopy(tc.path, uid); got != tc.want {
			t.Errorf("%s: isAppleContainerInheritedCopy = %v, want %v", tc.name, got, tc.want)
		}
	}
}
