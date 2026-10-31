package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hostfonts_test.go drives share_host_fonts (OQ-NX9, docs/design/provisioner-sets.md) through a
// real jail: with the user-scope opt-in, the host user's font folder (~/.local/share/fonts on
// Linux, ~/Library/Fonts on a Mac, which is the only folder a Mac host shares) is bound read-only
// under the jail's ~/.local/share/fonts, the jail's own fontconfig lists the font in it, a write
// to it is refused, and the launch names the folder. The unit suite pins the argv and the
// disclosure at their call site (internal/cli/run/hostfonts_test.go); this is the one place the
// image's fontconfig is shown to find the bind.
func TestSharedHostFontsAreReadOnlyAndFontconfigFindsThem(t *testing.T) {
	requireJail(t)
	packHome(t, `{"share_host_fonts": true}`)
	fonts := filepath.Join(os.Getenv("HOME"), ".local", "share", "fonts")
	if runtime.GOOS == "darwin" {
		fonts = filepath.Join(os.Getenv("HOME"), "Library", "Fonts")
	}
	if err := os.MkdirAll(fonts, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real font, so fontconfig indexes it: the image's own DejaVu, copied under a new name.
	src := hostFontFixture(t)
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fonts, "YoloShared.ttf"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, `{"network": {"mode": "bridge"}}`)

	// Markers are ASSEMBLED by the shell so the echoed command cannot match them.
	r := runYolo(t, dir, strings.Join([]string{
		`fc-list : file | grep -c "yolo-host/YoloShared" | sed 's/^/LISTED-/'`,
		`echo "WRITE-$(touch ~/.local/share/fonts/yolo-host/nope 2>/dev/null && echo ALLOWED || echo REFUSED)"`,
	}, "; "))
	if r.rc != 0 {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	out := r.combined()
	if !strings.Contains(out, "LISTED-1") {
		t.Errorf("the jail's fontconfig does not list the shared font:\n%s", out)
	}
	if !strings.Contains(out, "WRITE-REFUSED") {
		t.Errorf("the shared font folder is writable from inside the jail:\n%s", out)
	}
	if !strings.Contains(r.stderr, "Sharing your fonts read-only (share_host_fonts): "+fonts+
		" → /home/agent/.local/share/fonts/yolo-host") {
		t.Errorf("the launch did not name the shared font folder:\n%s", r.stderr)
	}
	// ~/.fonts does not exist in this home: skipped, with its line. A Mac host never looks there.
	if runtime.GOOS != "darwin" && !strings.Contains(r.combined(), "share_host_fonts: "+filepath.Join(os.Getenv("HOME"), ".fonts")+" does not exist") {
		t.Errorf("the missing ~/.fonts was not reported:\n%s", r.combined())
	}
}

// hostFontFixture finds a TrueType font this machine already has, or skips.
func hostFontFixture(t *testing.T) string {
	t.Helper()
	for _, pattern := range []string{
		"/nix/store/*-dejavu-fonts-minimal-*/share/fonts/truetype/DejaVuSans.ttf",
		"/nix/store/*-dejavu-fonts-*/share/fonts/truetype/DejaVuSans.ttf",
		"/usr/share/fonts/*/DejaVuSans.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	} {
		if m, _ := filepath.Glob(pattern); len(m) > 0 {
			return m[0]
		}
	}
	t.Skip("no DejaVuSans.ttf on this machine to stand in for a user font")
	return ""
}
