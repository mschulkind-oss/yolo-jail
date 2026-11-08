package stateguard

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// workspace makes a workspace with a `.yolo`, a durable dir and a setup home holding two bind
// sources, and returns it, the home and the sources.
func workspace(t *testing.T) (ws, home string, sources []string) {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = paths.SetupHomeState(ws, "podman-self")
	for _, d := range []string{filepath.Join(ws, ".yolo", "durable"), filepath.Join(home, "claude"), filepath.Join(home, "local")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return ws, home, []string{home, filepath.Join(home, "claude"), filepath.Join(home, "local")}
}

func roles(f Finding) []string {
	var out []string
	for _, m := range f.Mismatches {
		s := string(m.Role)
		if m.Gone {
			s += ":gone"
		}
		out = append(out, s)
	}
	return out
}

func TestAStampMatchesUntilTheStateMoves(t *testing.T) {
	ws, home, sources := workspace(t)
	s := Take(ws, home, sources)
	if s == nil || len(s.Entries) != 5 {
		t.Fatalf("stamp = %+v, want .yolo, durable, the home and its two sources", s)
	}
	if f := Check(s); f.Moved() {
		t.Fatalf("an untouched workspace mismatched: %v", roles(f))
	}
	// The incident: `.yolo` moved away, and then made again by something writing a log.
	if err := os.Rename(filepath.Join(ws, ".yolo"), filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	f := Check(s)
	if !f.StateMoved() || !f.HomesLost() || len(f.Mismatches) != 5 {
		t.Fatalf("a moved .yolo: %v", roles(f))
	}
	if err := os.MkdirAll(filepath.Join(ws, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	f = Check(s)
	if !f.StateMoved() || f.Mismatches[0].Gone {
		t.Fatalf("a .yolo made again in its place must mismatch as replaced: %v", roles(f))
	}
}

func TestOnlyTheDurableDirMovingLosesNoHome(t *testing.T) {
	ws, home, sources := workspace(t)
	s := Take(ws, home, sources)
	durable := filepath.Join(ws, ".yolo", "durable")
	if err := os.Rename(durable, durable+".old"); err != nil {
		t.Fatal(err)
	}
	f := Check(s)
	if !f.Moved() || !f.DurableMoved() || f.HomesLost() || f.StateMoved() {
		t.Fatalf("durable alone: %v", roles(f))
	}
}

func TestAHomeSourceReplacedLosesAHome(t *testing.T) {
	ws, home, sources := workspace(t)
	s := Take(ws, home, sources)
	claude := filepath.Join(home, "claude")
	// The replacement is made while the original still exists, so the two cannot share an inode
	// number: a remove-then-mkdir may be handed the freed number straight back (it was, on CI's
	// filesystem), which makes the "replaced" directory identical to the one stamped.
	replacement := claude + ".new"
	if err := os.Mkdir(replacement, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(claude); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, claude); err != nil {
		t.Fatal(err)
	}
	f := Check(s)
	if !f.HomesLost() || f.StateMoved() || len(f.Mismatches) != 1 || f.Mismatches[0].Path != claude {
		t.Fatalf("a replaced home source: %v", roles(f))
	}
}

func TestASourceOutsideTheStateIsNotStamped(t *testing.T) {
	ws, home, _ := workspace(t)
	outside := t.TempDir()
	s := Take(ws, home, []string{outside, filepath.Join(ws, ".yolo")})
	for _, e := range s.Entries {
		if e.Path == outside {
			t.Errorf("stamped %s, which a move of .yolo cannot take", outside)
		}
	}
}

func TestNoStampFindsNothing(t *testing.T) {
	if Check(nil).Moved() {
		t.Error("a jail with no stamp was reported moved")
	}
	if s := Take(t.TempDir(), "", nil); s != nil {
		t.Errorf("a workspace with no .yolo was stamped: %+v", s)
	}
}

// tarOf builds a tar stream of the given members.
func tarOf(t *testing.T, members []tar.Header, bodies map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range members {
		h := h
		body := bodies[h.Name]
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestARescueRecreatesTheStateDirAndExtractsUnderTheHomesName(t *testing.T) {
	ws, _, _ := workspace(t)
	if err := os.RemoveAll(filepath.Join(ws, ".yolo")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	r, err := NewRescue(ws, now)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := os.Stat(filepath.Join(ws, ".yolo", paths.WorkspaceStateIgnoreName)); err != nil {
		t.Errorf("the rescue made .yolo without its ignore file: %v", err)
	}
	stream := tarOf(t, []tar.Header{
		{Name: ".claude/", Typeflag: tar.TypeDir, Mode: 0o700},
		{Name: ".claude/projects/session.jsonl", Typeflag: tar.TypeReg, Mode: 0o600},
		{Name: ".claude/link", Typeflag: tar.TypeSymlink, Linkname: "/etc"},
		{Name: ".claude/hard", Typeflag: tar.TypeLink, Linkname: ".claude/projects/session.jsonl"},
		{Name: "../escape", Typeflag: tar.TypeReg},
		{Name: "other/file", Typeflag: tar.TypeReg},
		{Name: ".claudex/file", Typeflag: tar.TypeReg},
	}, map[string]string{".claude/projects/session.jsonl": "the session", "../escape": "x", "other/file": "y", ".claudex/file": "z"})
	n, err := r.Extract(stream, ".claude", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len("the session")) {
		t.Errorf("wrote %d bytes", n)
	}
	got, err := os.ReadFile(filepath.Join(r.HomeDir(), "claude", "projects", "session.jsonl"))
	if err != nil || string(got) != "the session" {
		t.Fatalf("the session file: %q, %v", got, err)
	}
	if target, err := os.Readlink(filepath.Join(r.HomeDir(), "claude", "link")); err != nil || target != "/etc" {
		t.Errorf("the link was not kept as itself: %q, %v", target, err)
	}
	if b, _ := os.ReadFile(filepath.Join(r.HomeDir(), "claude", "hard")); string(b) != "the session" {
		t.Errorf("the hard link: %q", b)
	}
	for _, stray := range []string{filepath.Join(r.Dir, "..", "escape"), filepath.Join(r.HomeDir(), "other"), filepath.Join(r.HomeDir(), "claudex")} {
		if _, err := os.Lstat(stray); err == nil {
			t.Errorf("a member outside the bind was written: %s", stray)
		}
	}
	if want := filepath.Join(ws, ".yolo", "rescue", "20261107T120000Z"); r.Dir != want {
		t.Errorf("rescue dir %s, want %s", r.Dir, want)
	}
	again, err := NewRescue(ws, now)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Dir == r.Dir {
		t.Error("a second rescue in the same second reused the first's directory")
	}
}

func TestExtractNeverWritesThroughALinkOutOfTheRescue(t *testing.T) {
	ws, _, _ := workspace(t)
	r, err := NewRescue(ws, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	outside := t.TempDir()
	stream := tarOf(t, []tar.Header{
		{Name: "local/", Typeflag: tar.TypeDir},
		{Name: "local/out", Typeflag: tar.TypeSymlink, Linkname: outside},
		{Name: "local/out/planted", Typeflag: tar.TypeReg},
	}, map[string]string{"local/out/planted": "x"})
	_, _ = r.Extract(stream, "local", "local")
	if _, err := os.Lstat(filepath.Join(outside, "planted")); err == nil {
		t.Fatal("the extraction wrote through a link out of the rescue")
	}
}

func TestARescueRefusesALinkedStateDir(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(ws, ".yolo")); err != nil {
		t.Fatal(err)
	}
	if r, err := NewRescue(ws, time.Now()); err == nil {
		r.Close()
		t.Fatal("a rescue wrote under a linked .yolo")
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("the linked target was written: %v", entries)
	}
}

func TestListNamesEachRescueWithItsSizeAndRestore(t *testing.T) {
	ws, home, _ := workspace(t)
	r, err := NewRescue(ws, time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	stream := tarOf(t, []tar.Header{{Name: ".pi/s", Typeflag: tar.TypeReg}}, map[string]string{".pi/s": "12345"})
	if _, err := r.Extract(stream, ".pi", "pi"); err != nil {
		t.Fatal(err)
	}
	rel, _ := paths.HomeStateRel(ws, home)
	if err := r.WriteRecord(RescueRecord{Created: time.Now(), HomeRel: rel, Copied: []string{"pi"}}); err != nil {
		t.Fatal(err)
	}
	r.Close()
	// A rescue whose record the jail rewrote to name a path that is none of the workspace's homes.
	forged, err := NewRescue(ws, time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := forged.WriteRecord(RescueRecord{HomeRel: "../../../.ssh"}); err != nil {
		t.Fatal(err)
	}
	forged.Close()

	got := List(ws)
	if len(got) != 2 {
		t.Fatalf("listed %d rescues, want 2: %+v", len(got), got)
	}
	recLen := func(dir string) int64 {
		fi, err := os.Stat(filepath.Join(dir, rescueRecordName))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	if got[0].Err != nil || got[0].Bytes != 5+recLen(got[0].Dir) {
		t.Errorf("size %d, %v", got[0].Bytes, got[0].Err)
	}
	if want := RestoreCommand(got[0].Dir, home); got[0].Restore != want {
		t.Errorf("restore %q, want %q", got[0].Restore, want)
	}
	if got[1].Restore != "" {
		t.Errorf("a forged record yielded a restore command: %q", got[1].Restore)
	}
}

func TestNoHomeReaperListsARescue(t *testing.T) {
	// The reapers that walk a workspace's homes (old agent logs, prune) read
	// paths.WorkspaceHomeStates; a rescue mirrors a home, and must never be one of them.
	ws, home, _ := workspace(t)
	r, err := NewRescue(ws, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	for _, h := range paths.WorkspaceHomeStates(ws) {
		if strings.Contains(h, RescueDirName) {
			t.Errorf("a rescue is listed as a home: %s", h)
		}
	}
	if hs := paths.WorkspaceHomeStates(ws); len(hs) != 1 || hs[0] != home {
		t.Errorf("homes = %v", hs)
	}
}

func TestRestoreCommandCopiesTheRescuedHomeOverTheHome(t *testing.T) {
	got := RestoreCommand("/w s/.yolo/rescue/x", "/w s/.yolo/setups/podman-self/home")
	want := "cp -a '/w s/.yolo/rescue/x/home/.' '/w s/.yolo/setups/podman-self/home'"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestTheWarningNamesTheStepForTheSetup(t *testing.T) {
	ws, home, sources := workspace(t)
	s := Take(ws, home, sources)
	if err := os.Rename(filepath.Join(ws, ".yolo"), filepath.Join(t.TempDir(), "x")); err != nil {
		t.Fatal(err)
	}
	f := Check(s)
	mounted := strings.Join(Warning(f, ws, Mounted), "\n")
	for _, want := range []string{"still intact inside the running jail", "mv <where you moved it> " + filepath.Join(ws, ".yolo"), filepath.Join(ws, ".yolo", "rescue")} {
		if !strings.Contains(mounted, want) {
			t.Errorf("the mounted warning lacks %q:\n%s", want, mounted)
		}
	}
	moved := strings.Join(Warning(f, ws, Moved), "\n")
	if !strings.Contains(moved, "moved with it") || strings.Contains(moved, "rescue") {
		t.Errorf("the macos-user warning:\n%s", moved)
	}
	if Warning(Finding{}, ws, Mounted) != nil {
		t.Error("nothing moved, yet a warning")
	}
	for _, l := range append(Warning(f, ws, Mounted), Warning(f, ws, Moved)...) {
		for _, jargon := range []string{"keeper", "notch", "surface", "SD-D"} {
			if strings.Contains(l, jargon) {
				t.Errorf("the warning says %q: %s", jargon, l)
			}
		}
	}
}

func TestTheStampSurvivesJSON(t *testing.T) {
	ws, home, sources := workspace(t)
	s := Take(ws, home, sources)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Stamp
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if Check(&back).Moved() {
		t.Error("a stamp read back from its record mismatched an untouched workspace")
	}
}
