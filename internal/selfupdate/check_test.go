package selfupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.11.0", "0.10.0", true},
		{"0.10.1", "0.10.0", true},
		{"1.0.0", "0.99.99", true},
		{"0.10.0", "0.10.0", false},
		{"0.9.9", "0.10.0", false},
		{"v0.11.0", "0.10.0", true},
		// A from-HEAD build's metadata does not make it newer than its release.
		{"0.10.0", "0.10.0+510.gcfefa8bf", false},
		{"0.10.0+510.gcfefa8bf", "0.10.0", false},
		// A pre-release sorts before its release.
		{"0.11.0", "0.11.0-rc1", true},
		{"0.11.0-rc1", "0.11.0", false},
		{"0.11.0-rc1", "0.10.0", true},
		// Unparsable is never newer.
		{"garbage", "0.10.0", false},
		{"0.11", "0.10.0", false},
		{"0.11.0", "", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if !AtLeast("0.11.0+3.gabc", "0.11.0") {
		t.Error("build metadata must not make an installed release older")
	}
	if AtLeast("0.10.0", "0.11.0") {
		t.Error("an older installed release must fail verification")
	}
}

func fixedNow() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) }

func releaseServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("GitHub's API refuses requests without a User-Agent")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckRelease(t *testing.T) {
	ch := Channel{Kind: KindHomebrew, Exe: "/opt/homebrew/Cellar/yolo-jail/0.10.0/bin/yolo", Version: "0.10.0"}

	t.Run("newer release", func(t *testing.T) {
		srv := releaseServer(t, 200, `{"tag_name":"v0.11.0"}`)
		st := Check(context.Background(), ch, State{}, CheckDeps{HTTP: srv.Client(), ReleaseURL: srv.URL, Now: fixedNow})
		if !st.Available || st.Latest != "0.11.0" || st.Error != "" {
			t.Errorf("got %+v, want an available 0.11.0", st)
		}
		if st.Identity != ch.Identity() || !st.CheckedAt.Equal(fixedNow()) {
			t.Errorf("state not keyed to the binary and the clock: %+v", st)
		}
	})
	t.Run("same release", func(t *testing.T) {
		srv := releaseServer(t, 200, `{"tag_name":"v0.10.0"}`)
		st := Check(context.Background(), ch, State{}, CheckDeps{HTTP: srv.Client(), ReleaseURL: srv.URL, Now: fixedNow})
		if st.Available || st.Error != "" {
			t.Errorf("got %+v, want up to date", st)
		}
	})
	t.Run("rate limited is an error, not an update", func(t *testing.T) {
		srv := releaseServer(t, 403, `{"message":"API rate limit exceeded"}`)
		st := Check(context.Background(), ch, State{}, CheckDeps{HTTP: srv.Client(), ReleaseURL: srv.URL, Now: fixedNow})
		if st.Available || !strings.Contains(st.Error, "403") {
			t.Errorf("got %+v, want a recorded 403", st)
		}
	})
	t.Run("an earlier no is kept for the same binary only", func(t *testing.T) {
		srv := releaseServer(t, 200, `{"tag_name":"v0.11.0"}`)
		deps := CheckDeps{HTTP: srv.Client(), ReleaseURL: srv.URL, Now: fixedNow}
		same := Check(context.Background(), ch, State{Identity: ch.Identity(), DeclinedFor: "0.11.0"}, deps)
		if same.DeclinedFor != "0.11.0" {
			t.Errorf("DeclinedFor dropped for the same binary: %+v", same)
		}
		other := Check(context.Background(), ch, State{Identity: "homebrew:0.9.0", DeclinedFor: "0.11.0"}, deps)
		if other.DeclinedFor != "" {
			t.Errorf("DeclinedFor carried over from a different binary: %+v", other)
		}
	})
}

// fakeGit answers each git invocation from a table keyed by its joined args.
type fakeGit struct {
	answers map[string]string
	fail    map[string]bool
	calls   []string
}

func (f *fakeGit) run(_ context.Context, dir string, args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, dir+": "+key)
	if f.fail[key] {
		return "", errors.New("exit status 128")
	}
	return f.answers[key], nil
}

const testTip = "abc1234def5678"

var testPaths = strings.Join(sourceUpdatePaths, " ")

func TestSourceUpdatePathsIncludeDeployInputs(t *testing.T) {
	for _, want := range []string{"Justfile", "scripts/build-go.sh", "scripts/stage-source-bundle.sh"} {
		if !slices.Contains(sourceUpdatePaths, want) {
			t.Errorf("sourceUpdatePaths omits %q", want)
		}
	}
}

func sourceGit() *fakeGit {
	return &fakeGit{
		answers: map[string]string{
			"symbolic-ref --short -q HEAD":                               "main",
			"config --get branch.main.remote":                            "origin",
			"config --get branch.main.merge":                             "refs/heads/main",
			"ls-remote --exit-code -- origin refs/heads/main":            testTip + "\trefs/heads/main",
			"rev-list --count cfefa8bf.." + testTip + " -- " + testPaths: "12",
			"rev-list --count HEAD.." + testTip + " -- " + testPaths:     "3",
		},
		// The tip is not local yet, so the check has to fetch it.
		fail: map[string]bool{"cat-file -e " + testTip + "^{commit}": true},
	}
}

func (f *fakeGit) ran(key string) bool {
	for _, c := range f.calls {
		if strings.HasSuffix(c, ": "+key) {
			return true
		}
	}
	return false
}

const quietFetch = "fetch --quiet --no-tags --no-write-fetch-head --refmap= -- origin refs/heads/main"

func TestCheckSource(t *testing.T) {
	ch := Channel{Kind: KindSource, Exe: "/home/u/.local/bin/yolo", SourceDir: "/src/yolo-jail", Branch: "main", Version: "cfefa8bf"}
	check := func(g *fakeGit, ch Channel) State {
		return Check(context.Background(), ch, State{}, CheckDeps{Git: g.run, Now: fixedNow})
	}

	t.Run("counts image-relevant commits from the binary's commit, writing no ref", func(t *testing.T) {
		g := sourceGit()
		st := check(g, ch)
		if !st.Available || st.Behind != 12 || st.Upstream != "origin/main" || st.Latest != "abc1234d" {
			t.Errorf("got %+v, want 12 commits behind origin/main", st)
		}
		if !g.ran(quietFetch) {
			t.Errorf("want the ref-less fetch %q; calls: %v", quietFetch, g.calls)
		}
		for _, c := range g.calls {
			if strings.HasSuffix(c, ": fetch --quiet") || strings.Contains(c, "pull") {
				t.Errorf("a plain fetch/pull moves the user's refs: %q", c)
			}
		}
	})
	t.Run("a tip already present needs no fetch at all", func(t *testing.T) {
		g := sourceGit()
		g.fail = nil
		check(g, ch)
		if g.ran(quietFetch) {
			t.Error("fetched a tip that was already local")
		}
	})
	t.Run("a stamped commit rebased away falls back to HEAD", func(t *testing.T) {
		g := sourceGit()
		g.fail["cat-file -e cfefa8bf^{commit}"] = true
		if st := check(g, ch); st.Behind != 3 {
			t.Errorf("got %+v, want HEAD's count of 3", st)
		}
	})
	t.Run("up to date, e.g. only docs changed upstream", func(t *testing.T) {
		g := sourceGit()
		g.answers["rev-list --count cfefa8bf.."+testTip+" -- "+testPaths] = "0"
		if st := check(g, ch); st.Available || st.Error != "" {
			t.Errorf("got %+v, want up to date", st)
		}
	})
	t.Run("a switched branch is refused", func(t *testing.T) {
		g := sourceGit()
		g.answers["symbolic-ref --short -q HEAD"] = "feat/x"
		st := check(g, ch)
		if st.Available || !strings.Contains(st.Error, `on "feat/x"`) {
			t.Errorf("got %+v, want a branch refusal", st)
		}
		if g.ran("ls-remote --exit-code -- origin refs/heads/main") {
			t.Error("a refused checkout must not touch the network")
		}
	})
	t.Run("no recorded branch accepts whatever is checked out", func(t *testing.T) {
		g := sourceGit()
		g.answers["symbolic-ref --short -q HEAD"] = "main"
		unrecorded := ch
		unrecorded.Branch = ""
		if st := check(g, unrecorded); st.Error != "" {
			t.Errorf("got %+v", st)
		}
	})
	t.Run("no upstream branch is reported", func(t *testing.T) {
		g := sourceGit()
		g.fail["config --get branch.main.remote"] = true
		st := check(g, ch)
		if !strings.Contains(st.Error, "no upstream") {
			t.Errorf("got %+v, want a no-upstream error", st)
		}
	})
	t.Run("detached HEAD is reported", func(t *testing.T) {
		g := sourceGit()
		g.fail["symbolic-ref --short -q HEAD"] = true
		st := check(g, ch)
		if !strings.Contains(st.Error, "detached HEAD") {
			t.Errorf("got %+v, want a detached-HEAD error", st)
		}
	})
	t.Run("offline is transient", func(t *testing.T) {
		g := sourceGit()
		g.fail["ls-remote --exit-code -- origin refs/heads/main"] = true
		st := check(g, ch)
		if st.Available || !strings.Contains(st.Error, "ls-remote") {
			t.Errorf("got %+v, want a transient ls-remote error", st)
		}
	})
}

func TestUnattendedGitEnvCannotAsk(t *testing.T) {
	env := unattendedGitEnv([]string{
		"HOME=/home/u", "SSH_AUTH_SOCK=/tmp/agent", "GIT_ASKPASS=/usr/bin/ksshaskpass",
		"SSH_ASKPASS=/usr/bin/ssh-askpass", "SSH_ASKPASS_REQUIRE=force", "GIT_TERMINAL_PROMPT=1",
	})
	got := strings.Join(env, " ")
	for _, gone := range []string{"GIT_ASKPASS=/", "SSH_ASKPASS=/", "SSH_ASKPASS_REQUIRE=force", "GIT_TERMINAL_PROMPT=1"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s survived: %s", gone, got)
		}
	}
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "SSH_ASKPASS_REQUIRE=never", "GCM_INTERACTIVE=never", "SSH_AUTH_SOCK=/tmp/agent"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s: %s", want, got)
		}
	}
}

func TestRunGitUsesAHardenedEnvironment(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	body := `#!/bin/sh
printf 'args:%s\n' "$*"
printf 'git_dir:%s\n' "${GIT_DIR-unset}"
printf 'git_askpass:%s\n' "${GIT_ASKPASS-unset}"
printf 'ssh_askpass:%s\n' "${SSH_ASKPASS-unset}"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GIT_DIR", "/tmp/other")
	t.Setenv("GIT_ASKPASS", "/tmp/gui")
	t.Setenv("SSH_ASKPASS", "/tmp/ssh-gui")

	out, err := runGit(context.Background(), "/src/yolo-jail", "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"args:-C /src/yolo-jail -c credential.interactive=false -c core.askPass= status --porcelain",
		"git_dir:unset",
		"git_askpass:",
		"ssh_askpass:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("runGit output missing %q:\n%s", want, out)
		}
	}
}

// The refusal's `yolo update --from <checkout>` is for pasting, so a checkout under a path with
// a space in it has to come back out of a shell as one word.
func TestSourceBranchMismatchNamesTheCheckoutAsOneShellWord(t *testing.T) {
	dir := "/Users/Jane Doe/code/yolo-jail"
	msg := SourceBranchMismatch(Channel{Kind: KindSource, SourceDir: dir, Branch: "main"}, "topic").Error()
	_, rest, _ := strings.Cut(msg, "`yolo update --from ")
	arg, _, ok := strings.Cut(rest, "`")
	if !ok {
		t.Fatalf("the refusal offers no `yolo update --from <checkout>`: %q", msg)
	}
	if got := testsupport.ShellWords(t, "yolo update --from "+arg); !slices.Equal(got,
		[]string{"yolo", "update", "--from", dir}) {
		t.Errorf("the offered command reads as %q in a shell: %q", got, msg)
	}
}
