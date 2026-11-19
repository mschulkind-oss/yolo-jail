package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrivateSuiteFiniteRecipesReachTheActualGuestColdAndFresh(t *testing.T) {
	requireJail(t)
	if !privateSuite.armed {
		t.Skip("requires explicit private suite receipt wrapper")
	}
	for _, tc := range []struct{ pack, bin, spec, version string }{
		{"opencode", "opencode", "opencode-ai", "0.0.0-fixture"},
		{"omp", "oh-omp", "@oh-labs/oh-omp@0.15.3", "0.15.3"},
		{"chrome-devtools", "chrome-devtools-mcp", "chrome-devtools-mcp", "0.0.0-fixture"},
		{"pinned", "cowsay", "cowsay@1.6.0", "1.6.0"},
	} {
		t.Run(tc.bin, func(t *testing.T) {
			if tc.pack == "omp" && runtime.GOARCH != "amd64" {
				t.Skip("unchanged official OMP declaration unsupported on this Linux architecture; empty filter separately checked")
			}
			dir := writeProject(t, `{}`)
			if tc.pack == "pinned" {
				pack := resolvedTempDir(t)
				if err := os.WriteFile(filepath.Join(pack, "pack.toml"), []byte("name = \"pinned\"\n[[program]]\nbin = \"cowsay\"\nvia = \"npm\"\npackage = \"cowsay@1.6.0\"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				packHome(t, `{"packs":["file://`+pack+`"],"agent_updates":false}`)
			} else {
				packHome(t, `{"packs":["`+tc.pack+`"],"agent_updates":false}`)
			}
			withPrivateFixtureYoloStore(t)
			for _, fresh := range []bool{false, true} {
				args := jailRunArgs()
				if fresh {
					args = append(args, "--update")
				}
				script := `printf '%s' "$YOLO_PROGRAM_CAPTURES" | jq -e '.["` + tc.bin + `"].declared == "` + tc.spec + `" and (.["` + tc.bin + `"].compatibility | test("^[0-9a-f]{64}$"))' >/dev/null || exit 90; command -v ` + tc.bin + `; ` + tc.bin + ` --version`
				r := runCommand(t, dir, append(args, "--", "bash", "-lc", script))
				argv := `["--version"]`
				if tc.pack == "chrome-devtools" {
					argv = `["--headless","--isolated","--chrome-arg=--no-sandbox","--chrome-arg=--disable-setuid-sandbox","--chrome-arg=--disable-gpu","--version"]`
				}
				if r.rc != 0 || !strings.Contains(r.stdout, "/.yolo/bin/launch/"+tc.bin+"\n") || !strings.Contains(r.stdout, "PRIVATE_OFFICIAL "+tc.bin+" "+tc.version+" "+argv) {
					t.Fatalf("normal inert %s guest fresh=%v: rc=%d\n%s", tc.bin, fresh, r.rc, r.combined())
				}
			}
			root, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".private-official-producer"))
			if err != nil {
				t.Fatal(err)
			}
			events, err := os.ReadFile(filepath.Join(string(root), "private-execution-events"))
			if err != nil || !strings.Contains(string(events), "bin="+tc.bin+" version="+tc.version+" phase=install") || !strings.Contains(string(events), "bin="+tc.bin+" version="+tc.version+" phase=ci") {
				t.Fatalf("actual private NPM executable events absent for %s: %v\n%s", tc.bin, err, events)
			}
			resolved, err := os.ReadFile(filepath.Join(string(root), "resolved-executables"))
			if err != nil || !strings.Contains(string(resolved), "node="+filepath.Join(string(root), "tools", "node")+" npm="+filepath.Join(string(root), "tools", "npm")) {
				t.Fatalf("actual resolved node/sibling npm did not remain private: %v\n%s", err, resolved)
			}
		})
	}
}
