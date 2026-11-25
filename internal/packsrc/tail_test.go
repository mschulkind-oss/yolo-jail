package packsrc

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// tail_test.go pins the store's side of the launch's progress tail (tail.go): that a refresh's
// mirror and a checkout's prefetch each WRITE to the tail they were handed, and that a store with
// none behaves as it always did. The tail itself, and what it draws, are internal/progress's.

// recordedTails is a TailOpener that keeps every stream it opened, and whether each was closed.
type recordedTails struct {
	mu      sync.Mutex
	streams map[string]*recordedStream
	order   []string
}

type recordedStream struct {
	bytes.Buffer
	closed bool
}

func (r *recordedTails) open(name string) io.WriteCloser {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streams == nil {
		r.streams = map[string]*recordedStream{}
	}
	s := &recordedStream{}
	r.streams[name] = s
	r.order = append(r.order, name)
	return &lockedStream{r: r, s: s}
}

type lockedStream struct {
	r *recordedTails
	s *recordedStream
}

func (l *lockedStream) Write(p []byte) (int, error) {
	l.r.mu.Lock()
	defer l.r.mu.Unlock()
	return l.s.Write(p)
}

func (l *lockedStream) Close() error {
	l.r.mu.Lock()
	defer l.r.mu.Unlock()
	l.s.closed = true
	return nil
}

func (r *recordedTails) text(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.streams[name]; s != nil {
		return s.String()
	}
	return ""
}

// echoingGit is a git that says every clone and fetch it is asked for on stderr, with its whole
// argument list, and then runs the real one: a deterministic stand-in for the progress frames
// git prints on a network, which a file:// remote hardly does.
func echoingGit(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "echogit")
	script := "#!/bin/sh\ncase \" $* \" in *\" clone \"*|*\" fetch \"*) echo \"GITSAID $*\" >&2; echo \"$*\" >> \"$0.log\";; esac\nexec git \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// THE CALL SITE of the refresh: a launch's mirror opens one stream under the repository's short
// name, says what it is checking, carries the clone's own stderr, and closes the stream when the
// mirror is done. Delete the Open in refreshMirror and this fails.
func TestRefreshWritesEachMirrorsGitOutputToItsTail(t *testing.T) {
	f := newRefreshFixture(t)
	f.store.Git = echoingGit(t)
	var rec recordedTails
	outs, err := f.store.Refresh([]RefreshPack{{Name: "p", Source: f.source("main")}},
		RefreshOptions{LockPath: f.lock, Tail: rec.open})
	if err != nil || outs[0].Err != nil {
		t.Fatalf("Refresh: %v / %v", err, outs[0].Err)
	}
	if len(rec.order) != 1 {
		t.Fatalf("streams opened = %q, want one per mirror", rec.order)
	}
	name := rec.order[0]
	if want := RepoLabel(f.repo); name != want {
		t.Errorf("stream named %q, want the repository's short name %q", name, want)
	}
	got := rec.text(name)
	if !strings.Contains(got, "checking ") {
		t.Errorf("the tail never heard what the mirror was doing:\n%s", got)
	}
	if !strings.Contains(got, "GITSAID") || !strings.Contains(got, "clone --progress") {
		t.Errorf("the clone's stderr, run with --progress, did not reach the tail:\n%s", got)
	}
	if !rec.streams[name].closed {
		t.Error("the mirror's stream stayed open after the mirror finished")
	}
}

// A refresh with no tail asks git for no progress: the same arguments as before the tail existed.
func TestRefreshWithNoTailAsksGitForNoProgress(t *testing.T) {
	f := newRefreshFixture(t)
	f.store.Git = echoingGit(t)
	if outs := f.refresh(t, false, RefreshPack{Name: "p", Source: f.source("main")}); outs[0].Err != nil {
		t.Fatal(outs[0].Err)
	}
	ran, err := os.ReadFile(f.store.Git + ".log")
	if err != nil || !strings.Contains(string(ran), "clone") {
		t.Fatalf("the echo git never saw the clone: %v %q", err, ran)
	}
	if strings.Contains(string(ran), "--progress") {
		t.Errorf("a store with no tail passed --progress:\n%s", ran)
	}
	if got := (&Store{}).progressArgs("fetch", "x"); strings.Join(got, " ") != "fetch x" {
		t.Errorf("progressArgs with no tail = %q", got)
	}
}

// THE CALL SITE of the checkout: the prefetch of a checkout's file contents (the slow half of
// staging a fetched pack; one prefetchBlobs serves a refresh's checkout and a launch's staging)
// runs with the store's tail, so what git says reaches it.
func TestAPrefetchWritesItsFetchToTheTail(t *testing.T) {
	f := newRefreshFixture(t)
	gitIn(t, f.repo, "config", "uploadpack.allowFilter", "true")
	f.store.Git = echoingGit(t)
	var rec recordedTails
	outs, err := f.store.Refresh([]RefreshPack{{Name: "p", Source: f.source("main")}},
		RefreshOptions{LockPath: f.lock, Tail: rec.open})
	if err != nil || outs[0].Err != nil {
		t.Fatalf("Refresh: %v / %v", err, outs[0].Err)
	}
	got := rec.text(rec.order[0])
	if !strings.Contains(got, "fetch --progress origin --no-tags") || !strings.Contains(got, "--filter=blob:none --stdin") {
		t.Errorf("the checkout's prefetch did not write to the tail, or ran with no --progress:\n%s", got)
	}
}

func TestFailedRunErrorsCollapseProgressFrames(t *testing.T) {
	in := "Receiving objects:  1%\rReceiving objects: 50%\rReceiving objects: 100%\nfatal: boom\n"
	got := lastFrames(in)
	if strings.Contains(got, "1%") || !strings.Contains(got, "100%") || !strings.Contains(got, "fatal: boom") {
		t.Errorf("lastFrames = %q", got)
	}
	if lastFrames("plain\n") != "plain\n" {
		t.Error("a stderr with no frames changed")
	}
}

func TestRepoLabel(t *testing.T) {
	for in, want := range map[string]string{
		"ssh://git@host/yolo-packs/matt.git": "yolo-packs/matt",
		"git@host:yolo-packs/matt.git":       "yolo-packs/matt",
		"https://github.com/a/b/c/":          "b/c",
		"file:///tmp/a/b":                    "a/b",
	} {
		if got := RepoLabel(in); got != want {
			t.Errorf("RepoLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTailRunPointsAFetchAtTheTail(t *testing.T) {
	var buf bytes.Buffer
	s := (&Store{}).WithTail(&buf)
	cmd := s.gitCmd(t.Context(), "", "fetch", "origin")
	s.tailRun(cmd)
	if cmd.Stderr != io.Writer(&buf) || strings.Join(cmd.Args[len(cmd.Args)-3:], " ") != "fetch --progress origin" {
		t.Errorf("tailRun left the command at stderr %v args %v", cmd.Stderr, cmd.Args)
	}
}
