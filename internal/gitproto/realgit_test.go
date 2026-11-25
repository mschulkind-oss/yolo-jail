package gitproto

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// These tests hold the package to the real git: a local `git push` runs a fake receive-pack
// (this test binary, re-run as a helper) that advertises one ref, reads the push with
// ReadCommands, drains the pack, and answers with WriteRefusal. Git must send what
// ReadCommands parses, and must print the refusal as its own "! [remote rejected]" line.

const helperEnv = "GITPROTO_RECEIVE_PACK_HELPER"

// TestReceivePackHelper is the fake receive-pack; it does nothing unless run as the helper.
func TestReceivePackHelper(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		t.Skip("run only as git's receive-pack")
	}
	os.Exit(fakeReceivePack(os.Stdin, os.Stdout))
}

func fakeReceivePack(in io.Reader, out io.Writer) int {
	w := NewWriter(out)
	head := os.Getenv("GITPROTO_HEAD")
	_ = w.Line(head + " refs/heads/main\x00report-status side-band-64k push-options delete-refs ofs-delta\n")
	_ = w.Flush()
	cmds, caps, opts, err := ReadCommands(NewReader(in))
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		return 2
	}
	if len(cmds) == 0 {
		return 0
	}
	if NeedsPack(cmds) {
		_, _ = io.Copy(io.Discard, in)
	}
	rec, _ := json.Marshal(map[string]any{"cmds": cmds, "caps": caps, "opts": opts})
	_ = os.WriteFile(os.Getenv("GITPROTO_RECORD"), rec, 0o600)
	if err := WriteRefusal(out, caps, cmds, "needs approval, which this version cannot ask for"); err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		return 2
	}
	return 0
}

type pushRecord struct {
	Cmds []Command
	Caps Caps
	Opts []string
}

func realGit(t *testing.T) (run func(dir string, args ...string) (string, string, error), home string) {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH")
	}
	home = t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid"}
	return func(dir string, args ...string) (string, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(env, extraEnv...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}, home
}

// extraEnv is set per test before running git; the tests in this file do not run in parallel.
var extraEnv []string

func TestRealGitPushReadsAndIsRefused(t *testing.T) {
	git, home := realGit(t)
	repo := filepath.Join(home, "repo")
	if _, e, err := git(home, "init", "-q", "-b", "main", repo); err != nil {
		t.Fatalf("git init: %v %s", err, e)
	}
	if _, e, err := git(repo, "commit", "-q", "--allow-empty", "-m", "one"); err != nil {
		t.Fatalf("git commit: %v %s", err, e)
	}
	head, _, _ := git(repo, "rev-parse", "HEAD")
	head = strings.TrimSpace(head)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(home, "fake-receive-pack")
	body := "#!/bin/sh\nexec \"$GITPROTO_TEST_BINARY\" -test.run='^TestReceivePackHelper$'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(home, "record.json")

	for _, tc := range []struct {
		name    string
		args    []string
		wantRef string
		create  bool
		delete  bool
		opts    int
	}{
		{name: "create", args: []string{"HEAD:refs/heads/agent/x"}, wantRef: "refs/heads/agent/x", create: true},
		{name: "push-option", args: []string{"-o", "ci.skip", "HEAD:refs/heads/agent/y"}, wantRef: "refs/heads/agent/y", create: true, opts: 1},
		{name: "delete only", args: []string{":refs/heads/main"}, wantRef: "refs/heads/main", delete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(record)
			extraEnv = []string{helperEnv + "=1", "GITPROTO_TEST_BINARY=" + exe, "GITPROTO_HEAD=" + head,
				"GITPROTO_RECORD=" + record}
			t.Cleanup(func() { extraEnv = nil })
			args := append([]string{"push", "--receive-pack=" + shquote.Quote(script), "file://" + filepath.Join(home, "nowhere")}, tc.args...)
			_, stderr, err := git(repo, args...)
			if err == nil {
				t.Fatalf("the refused push succeeded:\n%s", stderr)
			}
			want := "(needs approval, which this version cannot ask for)"
			if !strings.Contains(stderr, "! [remote rejected]") || !strings.Contains(stderr, want) {
				t.Fatalf("git did not print the refusal as its own line:\n%s", stderr)
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatalf("the helper recorded nothing: %v\n%s", err, stderr)
			}
			var rec pushRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				t.Fatal(err)
			}
			if len(rec.Cmds) != 1 || rec.Cmds[0].Ref != tc.wantRef || rec.Cmds[0].Create() != tc.create ||
				rec.Cmds[0].Delete() != tc.delete || len(rec.Opts) != tc.opts {
				t.Fatalf("git sent %+v", rec)
			}
			if !rec.Caps.Has("report-status") || !rec.Caps.Has("side-band-64k") {
				t.Fatalf("git negotiated %v", rec.Caps)
			}
		})
	}
}
