package gitremote

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

func TestPreflightHelperProcess(t *testing.T) {
	if os.Getenv("YOLO_TEST_GIT_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			os.Exit(Run(args[i+1:], Env{Getenv: os.Getenv, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
		}
	}
	os.Exit(64)
}

func gitPreflightFixture(t *testing.T) (string, string, string, string, string, <-chan []byte) {
	t.Helper()
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v: %s", args, e, b)
		}
		return strings.TrimSpace(string(b))
	}
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "bare")
	git(root, "init", "-q", work)
	git(root, "init", "-q", "--bare", bare)
	git(work, "config", "user.name", "Fixture")
	git(work, "config", "user.email", "fixture@example.invalid")
	if e := os.WriteFile(filepath.Join(work, "file"), []byte("first"), 0600); e != nil {
		t.Fatal(e)
	}
	git(work, "add", "file")
	git(work, "commit", "-qm", "first")
	git(work, "branch", "-M", "main")
	packets := make(chan []byte, 16)
	endpoint := serve(t, func(s *hostservice.Session) {
		service, _ := s.Request.Get("service")
		var name string
		if service == "git-upload-pack" {
			name = "upload-pack"
		} else {
			name = "receive-pack"
		}
		c := exec.Command("git", name, "--advertise-refs", bare)
		b, e := c.Output()
		if e != nil {
			s.Exit(1)
			return
		}
		s.StdoutBytes(b)
		var sent []byte
		for {
			f, e := frameproto.ReadFrame(s.Conn())
			if e != nil {
				break
			}
			if f.StreamID == frameproto.StreamStdout {
				if len(f.Payload) == 0 {
					break
				}
				sent = append(sent, f.Payload...)
			}
		}
		packets <- sent
		s.Exit(0)
	})
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	shim := filepath.Join(root, "git-remote-yolo")
	if e = os.WriteFile(shim, []byte("#!/bin/sh\nexec '"+bin+"' -test.run '^TestPreflightHelperProcess$' -- \"$@\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	return work, bare, "yolo::ssh://example.invalid/o/r", endpoint, root, packets
}

func TestPreflightDryRunBindsGitSelectionNotEmptyWirePush(t *testing.T) {
	work, _, remote, endpoint, shimDir, packets := gitPreflightFixture(t)
	env := append(os.Environ(), "YOLO_TEST_GIT_HELPER=1", EndpointEnv+"="+endpoint, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	c := exec.Command("git", "push", "--dry-run", "--porcelain", remote, "HEAD:refs/heads/main")
	c.Dir = work
	c.Env = env
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git dry-run: %v: %s", e, b)
	}
	if !strings.Contains(string(b), "*\tHEAD:refs/heads/main\t[new branch]") {
		t.Fatalf("porcelain: %q", b)
	}
	if got := <-packets; string(got) != "0000" {
		t.Fatalf("dry-run wire = %q, want only flush; no ref command", got)
	}
	// Production must bind the selected ref despite that empty wire update list.
	t.Setenv("YOLO_TEST_GIT_HELPER", "1")
	t.Setenv(EndpointEnv, endpoint)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	proposal, err := PreparePush(remote, []string{"HEAD:refs/heads/main"}, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.Commands) != 1 || proposal.Commands[0].Ref != "refs/heads/main" || !proposal.OldOIDsUntrusted {
		t.Fatalf("unexpected candidate: %+v", proposal)
	}
	if got := <-packets; string(got) != "0000" {
		t.Fatalf("production dry-run wire = %q", got)
	}
	<-packets // ls-remote upload-pack request
}
