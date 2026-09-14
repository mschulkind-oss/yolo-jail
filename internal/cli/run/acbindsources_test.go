package run

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE RULE FIVE CALL SITES EACH DISCOVERED SEPARATELY: on Apple Container a bind whose HOST
// side is not a directory does not arrive.
//
// It is written down five times in this package, in five different functions' comments —
// assemble.go on the user-env file ("Apple Container can't do single-file mounts under the
// ws_state parent mount without dropping it"), the same file on briefings, packfiles.go,
// hostfiles.go and packhostgrants.go — and each of those sites reached the rule by being
// broken first. backendcaps.go's own header names this shape exactly: "a rule reachable only
// from the function that discovered it will be re-discovered, or not". Five discoveries is
// four too many, so the rule is stated once here, over the argv, where a SIXTH emitter fails
// without having to be told.
//
// WHY A TEST AND NOT A PREDICATE. A predicate in backendcaps.go would need calling, and the
// defect is that a new emitter does not call it — the same reason machinetierparity_test.go
// diffs argvs instead of exporting a helper nobody would reach for. What this cannot do is
// answer WHICH escape a given site should take: acMaterialize a copy, refuse with a reason,
// or bind the parent directory instead. That is the author's call; this only insists one of
// them is made.
//
// WHAT IT DOES NOT COVER. Only `-v` on the container arms, and only what the FIXTURE causes
// to be emitted — a bind that appears solely under a config this fixture does not set is
// invisible here, which is the standing weakness of every argv test in this package. It also
// says nothing about podman, where a single-file bind is legitimate and used on purpose.
func TestNoAppleContainerBindHasANonDirectorySource(t *testing.T) {
	mounts := acBindSources(t)
	if len(mounts) < 4 {
		t.Fatalf("only %d binds on the Apple Container argv — the fixture or the extractor "+
			"has stopped producing them, so this test would pass by finding nothing", len(mounts))
	}

	dests := make([]string, 0, len(mounts))
	for d := range mounts {
		dests = append(dests, d)
	}
	sort.Strings(dests)

	for _, dest := range dests {
		src := mounts[dest]
		// A named volume (no leading slash) is not a host path at all — yolo-mise-data-v2 is
		// the one Apple Container gets — so there is nothing to stat.
		if !strings.HasPrefix(src, "/") {
			continue
		}
		st, err := os.Stat(src)
		if err != nil {
			// A source that is not there yet is a different defect and not this one: podman
			// would create it, and whether Apple Container does is a hardware question. Say
			// so rather than failing on it, so this test keeps one subject.
			t.Logf("note: %s → %s has no source on disk; this test says nothing about that",
				src, dest)
			continue
		}
		if st.IsDir() {
			continue
		}
		if reason, known := knownNonDirectoryACBinds[dest]; known {
			t.Logf("KNOWN DEFECT, still present: -v %s:%s — %s", src, dest, reason)
			continue
		}
		t.Errorf("Apple Container is given a bind whose host side is not a directory:\n"+
			"    -v %s:%s\n\n"+
			"That backend drops a non-directory bind rather than refusing it, so the file "+
			"simply is not there and the jail behaves as though nothing was configured. Five "+
			"functions in this package have already found this the hard way and each wrote "+
			"the rule into its own comment; this is the sixth.\n\n"+
			"Three escapes, all already used here: acMaterialize a copy into wsState, which "+
			"that backend binds whole at /home/agent; bind the containing DIRECTORY instead; "+
			"or skip it with a printed reason. Emitting it and hoping is the one option that "+
			"is not available.", src, dest)
	}

	// The ratchet's other half: a row whose bind is gone, or whose source has become a
	// directory, is a comment describing a bug that no longer exists — and the next
	// instance at that destination would be waved through by it.
	for dest, reason := range knownNonDirectoryACBinds {
		src, still := mounts[dest]
		if !still {
			t.Errorf("knownNonDirectoryACBinds still lists %s (%s) and the Apple Container "+
				"argv no longer binds it at all. Delete the row.", dest, reason)
			continue
		}
		if st, err := os.Stat(src); err == nil && st.IsDir() {
			t.Errorf("knownNonDirectoryACBinds still lists %s (%s) and its source %s is now a "+
				"directory. Delete the row.", dest, reason, src)
		}
	}
}

// knownNonDirectoryACBinds is the DEFECT list, not a waiver list — hostpathenv_test.go's
// header states the shape and why a row cannot outlive its bug.
//
// Both rows are the same site: assemble.go shadows two workspace files with `/dev/null` so
// the agent does not see the host's MCP config or an overmind socket it cannot use, and it
// does so with NO runtime gate — the bind is emitted on every backend. On Apple Container a
// non-directory source does not arrive, so the shadow is not applied and the agent sees the
// real file. That is the MILDEST member of this class, which is exactly why it survived: it
// degrades to "the shadow did not happen" rather than to "the jail is empty".
//
// ⚠ It is also the measured example of what backendparity_test.go's census structurally
// cannot see. There is no `rt ==` branch here to leave unclassified — the divergence is an
// ABSENT gate, and a grep over the source has nothing to match. Only the argv shows it.
//
// The fix is not this test's to make and is not obvious: /dev/null cannot be made a
// directory, and an empty directory bound over a JSON file is not a shadow either. The
// candidates are to skip the shadow on that backend with a printed reason (the shadow is a
// convenience, not a boundary), or to have the entrypoint write the empty file in-jail,
// which works on every backend and needs no bind at all.
var knownNonDirectoryACBinds = map[string]string{
	"/workspace/.vscode/mcp.json": "assemble.go's ungated /dev/null shadow bind",
	"/workspace/.overmind.sock":   "assemble.go's ungated /dev/null shadow bind",
}

// acBindSources assembles one Apple Container launch and returns dest→src for every `-v`.
//
// The fixture plants the two workspace files whose shadow binds are emitted with NO runtime
// gate at all (`-v /dev/null:/workspace/.vscode/mcp.json:ro` and the .overmind.sock twin).
// They are the reason this test is not hypothetical: a bind that no `rt ==` branch guards is
// invisible to backendparity_test.go's census by construction — it is one of the two
// "absence" shapes that census cannot see — and it is a non-directory source on every
// backend.
func acBindSources(t *testing.T) map[string]string {
	t.Helper()
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	o := goldenOptions(ws, home)
	o.IsMacOS = true
	o.IsLinux = false
	o.PathExists = func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".vscode"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(ws, ".vscode", "mcp.json"), filepath.Join(ws, ".overmind.sock")} {
		if err := os.WriteFile(f, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("security", sec),
		rt:           "container",
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   filepath.Join(ws, "agents"),
		wsState:      wsState,
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})

	out := map[string]string{}
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" && argv[i] != "--volume" {
			continue
		}
		src, dest, ok := strings.Cut(argv[i+1], ":")
		i++
		if !ok {
			continue
		}
		out[strings.TrimSuffix(dest, ":ro")] = src
	}
	return out
}
