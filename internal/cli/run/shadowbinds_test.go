package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The `/dev/null` shadow bind was the LAST ungated single-file bind in the assembler, and
// the one acbindsources_test.go's ratchet carried as a known defect.
//
// What the shadow is for: `<workspace>/.overmind.sock` is a host socket the jail cannot use
// (OVERMIND_SOCKET points at /tmp/overmind.sock instead). Binding /dev/null over it makes
// the path read as empty.
//
// ⚠ IT COVERED `<workspace>/.vscode/mcp.json` TOO UNTIL 2026-09-22, REMOVED AS A POSITION
// rather than as a bug fix: yolo does not shadow workspace MCP config, and an agent finding
// that file is DESIRED. It was never a boundary regardless (copilot loads three repo-root MCP
// files and Claude a fourth), and the costs that forced the decision are mechanical — the
// bind's character device (1:3) is a path git can neither hash nor `git add` on a TRACKED
// file, and it fired on file existence rather than on a selected reader. assemble.go's shadow
// block carries the long form. shadowFixture still PLANTS `.vscode/mcp.json` on purpose: a
// present file with NO bind emitted is what proves the removal, in the test that would
// otherwise pass for the wrong reason.
//
// Why Apple Container cannot have the remaining shadow — and the stated reason was WRONG,
// which matters because it is the reason a reader would use to decide whether the skip still
// applies.
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
// socket. So the SKIP IS STILL CORRECT and this test still guards the right thing; only the
// explanation moves, from "does not arrive" to "arrives and does not work".
//
// ⚠ AND WHY THE OBVIOUS FIX IS A DATA-LOSS BUG. acbindsources_test.go used to propose
// "have the entrypoint write the empty file in-jail, which works on every backend and needs
// no bind at all". It does not: `/workspace` is bound READ-WRITE — that is the whole point
// of the product — so `/workspace/.overmind.sock` IS the user's real file on the host, and
// writing an empty one truncates it. A shadow and a write are opposites here, and only the
// bind can tell them apart. That is why this is a skip.
func TestTheDevNullShadowIsSkippedOnAppleContainer(t *testing.T) {
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

			// The removal is pinned HERE rather than in a comment: the fixture PLANTS
			// `.vscode/mcp.json`, so a re-added bind lands in `got` and fails this loop.
			// Nothing else in the tree fails if it silently returns.
			for _, b := range got {
				if strings.HasSuffix(b, ":/workspace/.vscode/mcp.json:ro") {
					t.Errorf("the `.vscode/mcp.json` shadow is BACK: %q\n\n"+
						"It was removed on 2026-09-22 (assemble.go's shadow block has the long "+
						"form) as a POSITION: yolo does not shadow workspace MCP config, and an "+
						"agent finding it is desired. It was never a boundary (copilot reads "+
						"three repo-root MCP files, Claude a fourth), and its character-device "+
						"destination is a path git can neither hash nor `git add`. Do not re-add "+
						"it as an isolation measure — there is no such goal.", b)
				}
			}

			if tc.wantBinds {
				if len(got) != 1 || got[0] != "/dev/null:/workspace/.overmind.sock:ro" {
					t.Fatalf("podman must get exactly the one remaining shadow "+
						"(`/dev/null:/workspace/.overmind.sock:ro`), got %d: %v\n"+
						"Deleting it re-exposes a host socket the jail has no route to; adding "+
						"a second is the `.vscode/mcp.json` removal above.", len(got), got)
				}
				if strings.Contains(printed, "shadow") {
					t.Errorf("podman printed a shadow notice it has no reason to print:\n%s", printed)
				}
				return
			}

			if len(got) != 0 {
				t.Errorf("Apple Container is still given %d `/dev/null` shadow bind(s): %v\n\n"+
					"That backend renders the bind as a device node with the wrong major:minor, "+
					"so a read fails ENXIO instead of returning empty — an agent gets an I/O "+
					"error from a path the workspace says is a socket. Skip it and say so. Do NOT "+
					"'fix' this by writing the empty file from the entrypoint: /workspace is bound "+
					"read-write, so that truncates the user's own file on the host.", len(got), got)
			}
			// The skip is the fix only if it is disclosed — a launch has no quiet mode.
			if !strings.Contains(printed, ".overmind.sock") {
				t.Errorf("the Apple Container skip never named .overmind.sock on stderr.\n"+
					"An undisclosed drop is the defect this replaces, not the fix for it.\n"+
					"printed:\n%s", printed)
			}
			// And it must not name a shadow that no longer exists: a disclosure that
			// overstates what it dropped is as wrong as one that drops silently.
			if strings.Contains(printed, ".vscode/mcp.json") {
				t.Errorf("the Apple Container skip discloses `.vscode/mcp.json`, which is no "+
					"longer shadowed anywhere. The disclosure names a path it does not touch:\n%s", printed)
			}
		})
	}
}

// shadowFixture assembles one launch with every shadow SOURCE present, and returns the argv
// plus everything the assembler printed. Only `.overmind.sock` is still a shadow target;
// `.vscode/mcp.json` is planted to prove it is NOT one.
func shadowFixture(t *testing.T, rt string) (argv []string, printed string) {
	t.Helper()
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	// BOTH sources are planted. `.vscode/mcp.json` is no longer a shadow target (removed
	// 2026-09-22) and is here precisely so the test can assert NO bind was emitted for it;
	// `.overmind.sock` is the one remaining shadow.
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
