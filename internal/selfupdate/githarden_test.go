package selfupdate

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
)

// A remote or merge ref spelled like an option never reaches git: whatever
// can write the checkout writes its git config, and `--upload-pack=<cmd>` as a
// remote name makes git run <cmd>.
func TestCheckSourceRefusesAnOptionShapedUpstream(t *testing.T) {
	ch := Channel{Kind: KindSource, Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail", Branch: "main", Version: "cfefa8bf"}
	for name, key := range map[string]string{
		"remote": "config --get branch.main.remote",
		"merge":  "config --get branch.main.merge",
	} {
		t.Run(name, func(t *testing.T) {
			g := sourceGit()
			g.answers[key] = "--upload-pack=touch /tmp/pwned"
			st := Check(context.Background(), ch, State{}, CheckDeps{Git: g.run, Now: fixedNow})
			if st.Available || !strings.Contains(st.Error, `starts with "-"`) {
				t.Errorf("got %+v, want a refusal of the option-shaped upstream", st)
			}
			for _, c := range g.calls {
				if strings.Contains(c, "ls-remote") || strings.Contains(c, "fetch") {
					t.Errorf("an option-shaped upstream reached git: %q", c)
				}
			}
		})
	}
}

// The `--` is what keeps git from reading the remote as an option even if the
// refusal above were bypassed, so both network calls must carry it, right
// before the remote.
func TestCheckSourceEndsOptionsBeforeTheRemote(t *testing.T) {
	ch := Channel{Kind: KindSource, Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail", Branch: "main", Version: "cfefa8bf"}
	g := sourceGit()
	Check(context.Background(), ch, State{}, CheckDeps{Git: g.run, Now: fixedNow})
	for _, verb := range []string{"ls-remote", "fetch"} {
		found := false
		for _, c := range g.calls {
			if strings.Contains(c, ": "+verb+" ") {
				found = true
				if !strings.HasSuffix(c, " -- origin refs/heads/main") {
					t.Errorf("%s does not end its options before the remote: %q", verb, c)
				}
			}
		}
		if !found {
			t.Errorf("no %s call; calls: %v", verb, g.calls)
		}
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// isolateGitConfig keeps the real runGit away from the user's global and
// system git config, which a test must neither read nor depend on.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// The same attack through real git: a checkout whose config names
// `--upload-pack=<cmd>` as its remote. The check must fail without running it.
func TestCheckSourceWithRealGitNeverRunsAConfiguredUploadPack(t *testing.T) {
	requireGit(t)
	isolateGitConfig(t)
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "pwned")
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	gitIn(t, dir, "config", "branch.main.remote", "--upload-pack=touch "+marker+";")
	gitIn(t, dir, "config", "branch.main.merge", "refs/heads/main")
	head := gitIn(t, dir, "rev-parse", "HEAD")

	ch := Channel{Kind: KindSource, SourceDir: dir, Branch: "main", Version: head}
	st := Check(context.Background(), ch, State{}, CheckDeps{Git: runGit, Now: fixedNow})
	if st.Available || !strings.Contains(st.Error, `starts with "-"`) {
		t.Errorf("got %+v, want a refusal", st)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the configured upload-pack ran (stat err %v)", err)
	}

	// And the `--` alone holds: git takes the value as a repository name.
	if _, err := runGit(context.Background(), dir, "ls-remote", "--exit-code", "--", "--upload-pack=touch "+marker+";", "refs/heads/main"); err == nil {
		t.Error("ls-remote of an option-shaped repository name succeeded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("git read the remote after `--` as an option (stat err %v)", err)
	}
}

// The `--` must not break a real check: a clone one relevant commit behind its
// upstream counts it, through the real ls-remote and fetch.
func TestCheckSourceWithRealGitCountsUpstreamCommits(t *testing.T) {
	requireGit(t)
	isolateGitConfig(t)
	up := t.TempDir()
	gitIn(t, up, "init", "-q", "-b", "main")
	gitIn(t, up, "commit", "-q", "--allow-empty", "-m", "one")
	down := filepath.Join(t.TempDir(), "down")
	gitIn(t, up, "clone", "-q", up, down)
	built := gitIn(t, down, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(up, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(up, "cmd", "x.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, up, "add", "cmd/x.go")
	gitIn(t, up, "commit", "-q", "-m", "two")

	ch := Channel{Kind: KindSource, SourceDir: down, Branch: "main", Version: built}
	st := Check(context.Background(), ch, State{}, CheckDeps{Git: runGit, Now: fixedNow})
	if st.Error != "" || !st.Available || st.Behind != 1 || st.Upstream != "origin/main" {
		t.Errorf("got %+v, want one commit behind origin/main", st)
	}
	if got := gitIn(t, down, "rev-parse", "origin/main"); got != built {
		t.Errorf("the check moved origin/main to %s", got)
	}
}

// The command runGit runs is in its own session, with a process-group Cancel
// and a bounded wait: removing packsrc.DetachGit from gitCommand fails this.
func TestGitCommandIsDetachedAndBounded(t *testing.T) {
	cmd := gitCommand(t.Context(), "/src/yolo-jail", "ls-remote")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Error("git does not run in a new session, so ssh can prompt on the terminal")
	}
	if cmd.Cancel == nil {
		t.Error("no process-group Cancel: a timeout kills git but not its transport helper")
	}
	if cmd.WaitDelay <= 0 {
		t.Error("no WaitDelay: a helper holding the output pipe blocks Wait forever")
	}
}

// A remote that accepts the connection and never answers must not hang a
// source check past its deadline: once git is blocked on that remote, ending
// the check's context ends the check, promptly, as an ls-remote failure.
//
// The context ends when the remote has the connection, not on a clock started
// before Check. Such a clock also has to cover the local git runs before
// ls-remote (symbolic-ref and two config reads), and where starting git is
// slow they spend it: the check then fails as "no upstream branch" and never
// reaches the remote this is about. Ending it on contact is also the only way
// to know the stall was reached at all.
func TestCheckSourceEndsAtItsDeadlineAgainstAStalledRemote(t *testing.T) {
	requireGit(t)
	isolateGitConfig(t)
	// The request must reach this listener, not a proxy the machine names:
	// one that refuses or answers ends ls-remote before the stall.
	t.Setenv("no_proxy", "*")
	t.Setenv("NO_PROXY", "*")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener: %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	contacted := make(chan struct{})
	var contact sync.Once
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c) // held open, never answered
			mu.Unlock()
			contact.Do(func() { close(contacted) })
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})

	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	gitIn(t, dir, "remote", "add", "origin", "http://"+ln.Addr().String()+"/acme/yolo-jail.git")
	gitIn(t, dir, "config", "branch.main.remote", "origin")
	gitIn(t, dir, "config", "branch.main.merge", "refs/heads/main")
	ch := Channel{Kind: KindSource, SourceDir: dir, Branch: "main", Version: gitIn(t, dir, "rev-parse", "HEAD")}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan State, 1)
	go func() { result <- Check(ctx, ch, State{}, CheckDeps{Git: runGit, Now: fixedNow}) }()

	// A hang detector, not a budget: three local git runs, git and its
	// transport helper starting, and one loopback connect.
	select {
	case <-contacted:
	case st := <-result:
		t.Fatalf("the check ended before git reached the remote: %+v", st)
	case <-time.After(time.Minute):
		t.Fatal("git did not reach the remote within a minute")
	}

	// Half the WaitDelay on purpose: a Cancel that killed git alone would leave
	// its transport helper holding an output pipe until that backstop, and this
	// must tell the two apart. Killing and reaping the process group takes
	// milliseconds; the rest is room for a loaded machine.
	bound := gitCommand(t.Context(), dir).WaitDelay / 2
	ended := time.Now()
	cancel()
	select {
	case st := <-result:
		if took := time.Since(ended); took > bound {
			t.Errorf("the check took %s to end after its context did, against a stalled remote (bound %s)", took, bound)
		}
		if st.Available || !strings.Contains(st.Error, "ls-remote") {
			t.Errorf("got %+v, want an ls-remote failure", st)
		}
	case <-time.After(10 * bound):
		t.Fatalf("the check was still running %s after its context ended, against a stalled remote", 10*bound)
	}
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// Text from outside yolo is printed to a terminal, so an escape sequence in an
// upstream's name, a tag, or git's stderr is dropped before it is saved or
// shown.
func TestCheckStripsControlCharacters(t *testing.T) {
	ch := Channel{Kind: KindSource, Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail", Branch: "main", Version: "cfefa8bf"}
	answer := func(lsRemoteErr error) GitRunner {
		return func(_ context.Context, _ string, args ...string) (string, error) {
			key := strings.Join(args, " ")
			switch {
			case key == "symbolic-ref --short -q HEAD":
				return "main", nil
			case key == "config --get branch.main.remote":
				return "origin", nil
			case key == "config --get branch.main.merge":
				return "refs/heads/main\x1b[2J", nil
			case strings.HasPrefix(key, "ls-remote"):
				if lsRemoteErr != nil {
					return "", lsRemoteErr
				}
				return "abc1234d\x1b]0;x\x07ef\tref", nil
			case strings.HasPrefix(key, "rev-list"):
				return "2", nil
			}
			return "", nil
		}
	}

	st := Check(context.Background(), ch, State{}, CheckDeps{Git: answer(nil), Now: fixedNow})
	if st.Error != "" || hasControl(st.Upstream) || hasControl(st.Latest) {
		t.Errorf("control characters survived the check: %q", []string{st.Upstream, st.Latest, st.Error})
	}
	if n := Notice(st); hasControl(n) {
		t.Errorf("the notice carries control characters: %q", n)
	}

	st = Check(context.Background(), ch, State{}, CheckDeps{Git: answer(errors.New("exit status 128: \x1b[31mfatal\x1b[0m\nsecond line")), Now: fixedNow})
	if st.Error == "" || hasControl(st.Error) || !strings.Contains(st.Error, "fatal") {
		t.Errorf("error not stripped to readable text: %q", st.Error)
	}

	srv := releaseServer(t, 200, `{"tag_name":"v0.11.0\u001b[2J"}`)
	rel := Channel{Kind: KindHomebrew, Exe: "/opt/homebrew/bin/yolo", Version: "0.10.0"}
	st = Check(context.Background(), rel, State{}, CheckDeps{HTTP: srv.Client(), ReleaseURL: srv.URL, Now: fixedNow})
	if hasControl(st.Latest) {
		t.Errorf("a tag's control characters survived: %q", st.Latest)
	}
}

// A state file is stripped on the way in and out as well, so a cache written
// before this rule, or by hand, prints nothing a terminal would obey.
func TestStateFileIsStrippedOnSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	raw := `{"kind":"source","upstream":"origin/ma\u001b[2Jin","latest":"abc\u0007","error":"x\u001b]0;t\u0007","available":true}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadState(path)
	if hasControl(got.Upstream) || hasControl(got.Latest) || hasControl(got.Error) {
		t.Errorf("LoadState kept control characters: %+v", got)
	}

	if err := SaveState(path, State{Upstream: "o\x1b[1m/main", Latest: "a\x00b", Error: "e\x9b"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `\u001b`) || strings.Contains(string(data), `\u0000`) || strings.Contains(string(data), `\u009b`) {
		t.Errorf("SaveState wrote control characters: %s", data)
	}
}
