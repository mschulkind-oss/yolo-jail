package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two `/dev/null` shadow binds were the LAST ungated single-file bind in the
// assembler, and the one acbindsources_test.go's ratchet carried as a known defect.
//
// What the shadows are for: an agent that reads `<workspace>/.vscode/mcp.json` tries to
// start MCP servers the jail does not have, and `<workspace>/.overmind.sock` is a host
// socket it cannot use (OVERMIND_SOCKET points at /tmp/overmind.sock instead). Binding
// /dev/null over each makes them read as empty.
//
// Why Apple Container cannot have them — and the stated reason was WRONG, which matters
// because it is the reason a reader would use to decide whether the skip still applies.
//
// It said "a bind whose HOST side is not a directory does not arrive there". MEASURED
// 2026-09-14 (macOS 26.5 arm64, `container` 1.1.0,
// integration/applecontainer_test.go's TestAppleContainerBindsASingleFile): it DOES arrive.
// A regular-file bind works completely — content, write-through, `:ro`. And the /dev/null
// bind arrives too, as a `character special file`. What it does not do is WORK:
//
//	dev=0:3002        the node the bind created
//	realdev=1:3       the container's own /dev/null, which reads fine
//	read=[cat: /ws/shadowed.json: No such device or address]
//
// Apple Container synthesises the node through virtiofs with the wrong major:minor, so a
// read fails ENXIO instead of returning empty. The shadow's whole purpose is "reads as
// empty", and an I/O error is not that — an agent gets a broken file rather than an absent
// config. So the SKIP IS STILL CORRECT and this test still guards the right thing; only the
// explanation moves, from "does not arrive" to "arrives and does not work".
//
// ⚠ AND WHY THE OBVIOUS FIX IS A DATA-LOSS BUG. acbindsources_test.go used to propose
// "have the entrypoint write the empty file in-jail, which works on every backend and needs
// no bind at all". It does not: `/workspace` is bound READ-WRITE — that is the whole point
// of the product — so `/workspace/.vscode/mcp.json` IS the user's real file on the host, and
// writing an empty one truncates it. A shadow and a write are opposites here, and only the
// bind can tell them apart. That is why this is a skip.
func TestTheDevNullShadowsAreSkippedOnAppleContainer(t *testing.T) {
	for _, tc := range []struct {
		rt        string
		wantBinds bool
	}{
		{"podman", true},
		{"container", false},
	} {
		t.Run(tc.rt, func(t *testing.T) {
			argv, printed := shadowFixture(t, tc.rt)

			var got []string
			for i := 0; i+1 < len(argv); i++ {
				if argv[i] == "-v" && strings.HasPrefix(argv[i+1], "/dev/null:") {
					got = append(got, argv[i+1])
				}
			}

			if tc.wantBinds {
				if len(got) != 2 {
					t.Fatalf("podman must still get both shadows, got %d: %v\n"+
						"Without them the Apple Container half below passes vacuously.", len(got), got)
				}
				if strings.Contains(printed, "shadow") {
					t.Errorf("podman printed a shadow notice it has no reason to print:\n%s", printed)
				}
				return
			}

			if len(got) != 0 {
				t.Errorf("Apple Container is still given %d `/dev/null` shadow bind(s): %v\n\n"+
					"That backend drops a bind whose host side is not a directory, so the shadow "+
					"is not applied and the agent reads the real file — silently. Skip it and say "+
					"so. Do NOT 'fix' this by writing the empty file from the entrypoint: "+
					"/workspace is bound read-write, so that truncates the user's own file on the "+
					"host.", len(got), got)
			}
			// The skip is the fix only if it is disclosed — a launch has no quiet mode.
			for _, want := range []string{".vscode/mcp.json", ".overmind.sock"} {
				if !strings.Contains(printed, want) {
					t.Errorf("the Apple Container skip never named %s on stderr.\n"+
						"An undisclosed drop is the defect this replaces, not the fix for it.\n"+
						"printed:\n%s", want, printed)
				}
			}
		})
	}
}

// shadowFixture assembles one launch with both shadow sources present, and returns the argv
// plus everything the assembler printed.
func shadowFixture(t *testing.T, rt string) (argv []string, printed string) {
	t.Helper()
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	if err := os.MkdirAll(filepath.Join(ws, ".vscode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".vscode", "mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".overmind.sock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	o := goldenOptions(ws, home)
	// THE ASSEMBLER PRINTS TO STDERR (assemble.go: `out := o.pr(o.Stderr)`), which is where a
	// disclosure belongs — stdout is the jail command's own. A fixture capturing Stdout here
	// reads back empty and the disclosure half of this test passes vacuously.
	o.Stderr = &buf
	o.IsMacOS = rt == "container"
	o.IsLinux = !o.IsMacOS

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	argv = o.assembleRunCmd(&assembleInput{
		cfg:          newConfig(),
		rt:           rt,
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   filepath.Join(ws, "agents"),
		wsState:      wsState,
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	return argv, buf.String()
}
