package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The macos-user shell need not export NPM_CONFIG_PREFIX: its generated launcher supplies
// that default inside its own process. Reading only REAL_BIN from that launcher loses the
// default and makes this file-only probe look for /bin/pi instead of the installed bundle.
func TestProjectDirsProbeReadsTheLaunchersPrefixDefault(t *testing.T) {
	t.Setenv("YOLO_BYPASS_SHIMS", "1") // The probe reads a fixture tree with the real grep.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on PATH")
	}
	for _, tc := range []struct{ name, prefix string }{{"unset prefix", ""}, {"explicit prefix", "npm custom"}} {
		t.Run(tc.name, func(t *testing.T) {
			home := resolvedTempDir(t)
			t.Setenv("HOME", home)
			prefix := filepath.Join(home, ".npm-global")
			if tc.prefix != "" {
				prefix = filepath.Join(home, tc.prefix)
				t.Setenv("NPM_CONFIG_PREFIX", prefix)
			} else {
				t.Setenv("NPM_CONFIG_PREFIX", "")
			}
			pkg := filepath.Join(prefix, "lib/node_modules/fixture")
			bin := filepath.Join(prefix, "bin")
			launch := filepath.Join(home, ".yolo/bin/launch")
			for _, dir := range []string{pkg, bin, launch} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			contents := strings.Join(declaredProjectDirs("pi"), "\n")
			for name, contents := range map[string]string{
				filepath.Join(pkg, "package.json"): `{}`,
				filepath.Join(pkg, "fixture"):      contents,
				filepath.Join(launch, "pi"):        "export NPM_CONFIG_PREFIX=\"${NPM_CONFIG_PREFIX:-$HOME/.npm-global}\"\nREAL_BIN=\"$NPM_CONFIG_PREFIX/bin/$BIN\"\n",
			} {
				if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(filepath.Join(pkg, "fixture"), filepath.Join(bin, "pi")); err != nil {
				t.Fatal(err)
			}
			probe := projectDirsProbe(t, "pi", "pi")
			if probe == "" {
				t.Fatal("pi must declare a project skills directory to check")
			}
			cmd := exec.Command(bash, "-c", "true"+probe)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("file-only project-dir probe failed: %v\n%s", err, out)
			}
		})
	}
}
