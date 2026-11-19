package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateSuiteActualBackgroundAndTerminalEntryPathsConsumeTheInertProgram(t *testing.T) {
	requireJail(t)
	if !privateSuite.armed {
		t.Skip("requires explicit private suite receipt wrapper")
	}
	for _, route := range []string{"background", "migration-terminal", "keeper-terminal"} {
		t.Run(route, func(t *testing.T) {
			packHome(t, `{"packs":["opencode"],"agent_updates":false,"convert_later":true}`)
			withPrivateFixtureYoloStore(t)
			dir := writeProject(t, `{}`)
			script := `printf '%s' "$YOLO_PROGRAM_CAPTURES" | jq -e '.opencode.declared == "opencode-ai" and (.opencode.compatibility | test("^[0-9a-f]{64}$"))' >/dev/null || exit 90; command -v opencode; opencode --version`
			var output string
			var rc int
			switch route {
			case "background":
				r := startYoloBackgroundWith(t, route, dir, nil, script)
				rc, output = r.wait(t, jailTimeout()), r.combined()
			case "migration-terminal":
				r := launchAtTerminal(t, dir, append(jailRunArgs(), "--", "bash", "-lc", script), "")
				rc, output = r.rc, r.out
			case "keeper-terminal":
				r := startYoloAtTerminal(t, route, dir, script)
				rc, output = r.wait(t, jailTimeout()), r.combined()
			}
			if rc != 0 || !strings.Contains(output, "/.yolo/bin/launch/opencode") || !strings.Contains(output, `PRIVATE_OFFICIAL opencode 0.0.0-fixture ["--version"]`) {
				t.Fatalf("actual %s route did not consume inert capture: rc=%d\n%s", route, rc, output)
			}
			root, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".private-official-producer"))
			if err != nil {
				t.Fatal(err)
			}
			events, err := os.ReadFile(filepath.Join(string(root), "private-execution-events"))
			if err != nil || !strings.Contains(string(events), "bin=opencode version=0.0.0-fixture phase=install") {
				t.Fatalf("actual %s private executable event missing: %v\n%s", route, err, events)
			}
		})
	}
}
