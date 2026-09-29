package run

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// shippedSelfExecDaemonArgv matches a pack's `["yolo", "internal", "daemon", "<name>", …]`,
// the argv a launch self-execs as this test binary (execx.SelfExecArgv).
var shippedSelfExecDaemonArgv = regexp.MustCompile(`"yolo",\s*"internal",\s*"daemon",\s*"([a-z0-9-]+)"`)

// shippedSelfExecDaemons is every daemon name a shipped pack spells in that argv, read out
// of packs/ so a daemon added tomorrow is covered without editing this file.
func shippedSelfExecDaemons(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	root := filepath.Join(repoRoot(t), "packs")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !(strings.HasSuffix(p, ".json") || strings.HasSuffix(p, ".jsonc")) {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range shippedSelfExecDaemonArgv.FindAllSubmatch(body, -1) {
			seen[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	// aws-auth is the name whose missing dispatch this test was written for; a census
	// without it means the regexp or the manifests' spelling moved, not that it is fixed.
	if !seen["aws-auth"] || !seen["openai-auth-broker"] {
		t.Fatalf("found shipped self-exec daemons %v; aws-auth and openai-auth-broker are both "+
			"shipped, so the census is reading the wrong thing", names)
	}
	return names
}

// TestNoShippedDaemonSelfExecRunsTheSuite pins TestMain's daemon dispatch: every
// `internal daemon <name>` argv a shipped pack declares, exec'd as this test binary, must RUN
// THAT DAEMON — which, handed a flag it does not know, exits at once — and never fall
// through to m.Run.
//
// Falling through is how TestWorkspaceSkillsReachTheMacosUserHome and its neighbors
// flaked. TestMain used to name four daemons one by one, and aws-auth was not among them, so a
// macos-user launch that enables the aws-auth loophole spawned `<test binary> internal daemon
// aws-auth …` and the child ran this whole package instead. A host-wide daemon is spawned
// detached, so that child outlived the test that started it, with that test's HOME and this
// process's host-singleton dir. Its tests' cleanups SIGTERMed the OpenAI broker a parent test
// had just spawned (the PID file is shared), the parent read "exited at startup without
// binding its socket", and refused its launch with "OpenAI credential service did not start".
// The same child went on writing under the finished test's HOME and recreated it after that
// test's t.TempDir cleanup had begun (landing during the RemoveAll, that is a "directory not
// empty" failure), and its own copy of the aws-auth test spawned the next child, so the chain
// outlived the package run.
//
// A child still running at the deadline is the fall-through: a daemon rejecting a flag takes
// milliseconds, and the suite takes tens of seconds. The child gets a private HOME and
// host-singleton dir, so even that failure cannot reach this process's daemons.
func TestNoShippedDaemonSelfExecRunsTheSuite(t *testing.T) {
	for _, name := range shippedSelfExecDaemons(t) {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "internal", "daemon", name,
				"--yolo-selfexec-test-no-such-flag")
			private := t.TempDir()
			cmd.Env = append(os.Environ(),
				"HOME="+private,
				// testsupport's inherit variable: a child that DID run the suite would
				// otherwise share, and reap from, this process's singleton dir.
				"TESTSUPPORT_HOST_SINGLETON_DIR="+private)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("`<test binary> internal daemon %s` was still running after 20s: TestMain "+
					"did not dispatch it, so the child is re-running this package's suite. Route "+
					"it through internaldaemon.Run.\nlast of its output:\n%s", name, tail(out.String(), 2000))
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("`internal daemon %s --<unknown flag>` = %v, want a daemon refusing the "+
					"flag with a non-zero exit\n%s", name, err, out.String())
			}
			if s := out.String(); strings.Contains(s, "unknown daemon") ||
				strings.Contains(s, "=== RUN") || strings.Contains(s, "\nPASS") {
				t.Errorf("`internal daemon %s` did not reach its daemon:\n%s", name, s)
			}
		})
	}
}

// tail is the last n bytes of s: a child that ran the suite prints a line per launch.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
