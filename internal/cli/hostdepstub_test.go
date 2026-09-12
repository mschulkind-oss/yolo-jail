package cli

// hostdepstub_test.go is the package's TEST-SIDE PROTECTION for the host dependency gate
// (docs/design/report-tiers.md §4.9), and it exists because the gate turned two properties of
// the MACHINE into properties of the suite.
//
//  1. A MISSING DECLARED BINARY NOW REFUSES A WRITING APPLY. Before the gate, whether `claude`
//     was on the PATH of the machine running the tests changed one dim line in a report nobody
//     asserted on. Now it changes the exit code, so every fixture that runs `--assert` over a
//     pack declaring a `program` would pass in this development jail (which has all six agent
//     CLIs) and fail on CI (which has none). stubDeclaredBins makes the answer the fixture's.
//
//  2. AN ACCEPTED PROMPT RUNS A REAL INSTALLER. Measured while building the gate: a fixture
//     answering `y` to the loss confirmation had its answer read by the new prompt, and the
//     suite ran `npm install -g solocli` against the real registry. AGENTS.md's no-agent-tests
//     rule is the same rule one level down — an automated test must never install anything —
//     so TestMain below replaces the runner with one that refuses, and a test that wants to
//     exercise an install overrides the seam itself with something that cannot leave the
//     fixture.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestMain disarms the install runner for the WHOLE package.
//
// A default that refuses is the only safe one: the gate's prompt reads whatever stdin a
// fixture happened to provide, so any test that answers `y` to any confirmation is one
// `program` contribution away from running `npm install -g` or `curl … | sh` on the machine
// running the suite. Overriding the seam per test would protect the tests someone remembered
// to write it into.
func TestMain(m *testing.M) {
	depInstallRun = func(cmd string, _ io.Writer) error {
		return fmt.Errorf("test guard: refusing to run a pack's install hint %q — override "+
			"depInstallRun in your test if the install itself is what you are exercising", cmd)
	}
	os.Exit(m.Run())
}

// stubBins prepends a temp dir holding an executable stub per name to PATH, and returns it.
//
// PREPENDED, never replacing PATH: depcheck.DetectManager probes for apt/dnf/pacman/brew and
// the install runner needs a shell, so a fixture that owned the whole PATH would be answering
// a different question from the one a run asks. (fakeBinDir, in applyhostdeps_test.go, DOES
// replace PATH — that is its job: pinning exactly one detected manager.)
func stubBins(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if n == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// stubDeclaredBins makes every binary the SHIPPED packs declare resolvable — the six agent
// CLIs, today.
//
// ONLY the shipped ones, and the line is where machine-dependence is. A fixture pack declaring
// `program alphacli` is missing on every machine on earth, so its refusal is deterministic and
// the fixture can decide what it wants; `claude` is present in this development jail and absent
// on CI, and that is the difference that decides an exit code. A fixture that needs its OWN
// synthetic binary present calls stubBins with the name, which says so where it is read.
//
// DERIVED from packload.Embedded() rather than a written list of the six: a pack that grows a
// `program` tomorrow would otherwise turn a writing-apply fixture red on a machine lacking that
// tool, which is a fixture failure reported as a product failure.
func stubDeclaredBins(t *testing.T) {
	t.Helper()
	var names []string
	for _, p := range packload.Embedded() {
		for _, d := range p.Decl.DepRequirements() {
			names = append(names, d.Bin)
		}
	}
	stubBins(t, names...)
}
