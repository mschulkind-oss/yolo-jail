package helperpin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/logstamp"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

func writeHost(t *testing.T, root, goos, goarch, body string) string {
	t.Helper()
	dir := filepath.Join(root, "bin", goos+"-"+goarch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "yolo")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestResolveReadsTheHostBinaryFromTheResolvedRoot(t *testing.T) {
	base := realTemp(t)
	target := filepath.Join(base, "generation")
	writeHost(t, target, "linux", "amd64", "host-yolo")
	link := filepath.Join(base, "flake-bundle")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	pin, err := ResolveFor(link, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(target, "bin", "linux-amd64", "yolo")
	if pin.Path != want {
		t.Fatalf("pin path = %q, want %q", pin.Path, want)
	}
	if pin.Size != int64(len("host-yolo")) || pin.Ino == 0 {
		t.Fatalf("identity not recorded: %+v", pin)
	}
	if !pin.Unchanged() {
		t.Fatal("a fresh pin must be unchanged")
	}
}

func TestResolveUsesTheRunningPlatform(t *testing.T) {
	if _, err := Resolve(realTemp(t)); err == nil {
		t.Fatal("an empty root must not resolve")
	}
}

func TestResolveRefusesABundleWithoutAHostYolo(t *testing.T) {
	root := realTemp(t)
	_, err := ResolveFor(root, "linux", "amd64")
	if err == nil {
		t.Fatal("want an error for a bundle with no host yolo")
	}
	if !strings.Contains(err.Error(), reporoot.ReinstallSteps) {
		t.Fatalf("error does not name the reinstall steps: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(root, "bin", "linux-amd64", "yolo")) {
		t.Fatalf("error does not name the missing path: %v", err)
	}
}

func TestUnchangedSeesAReplacedFile(t *testing.T) {
	root := realTemp(t)
	p := writeHost(t, root, "linux", "amd64", "one")
	pin, err := ResolveFor(root, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	repl := p + ".new"
	if err := os.WriteFile(repl, []byte("two-two"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(repl, p); err != nil {
		t.Fatal(err)
	}
	if pin.Unchanged() {
		t.Fatal("a file renamed over the pin must read as changed")
	}
	pin2, err := ResolveFor(root, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if pin2.Unchanged() {
		t.Fatal("a removed pin must read as changed")
	}
}

func TestUnchangedSeesATouchedFile(t *testing.T) {
	root := realTemp(t)
	p := writeHost(t, root, "linux", "amd64", "one")
	pin, err := ResolveFor(root, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Unix(0, pin.MtimeNanos).Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if pin.Unchanged() {
		t.Fatal("a changed mtime must read as changed")
	}
}

func TestPinRoundTripsThroughJSON(t *testing.T) {
	root := realTemp(t)
	writeHost(t, root, "linux", "amd64", "one")
	pin, err := ResolveFor(root, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(pin)
	if err != nil {
		t.Fatal(err)
	}
	var back Pin
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back != pin {
		t.Fatalf("round trip differs: %+v vs %+v", back, pin)
	}
	if !back.Unchanged() {
		t.Fatal("a pin read back from JSON must still match the file")
	}
}

func TestStartLineFormat(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	got := formatStartLine(now, "keeper", "1.2.3", "abc123", 42, "", "/Users/a b/yolo", nil)
	want := "2026-11-10T12:00:00.000Z helper start: name=keeper build=1.2.3 commit=abc123 pid=42 jail=- exe=\"/Users/a b/yolo\""
	// The stamp is local time; compare everything after it.
	_, rest, _ := strings.Cut(got, " ")
	_, wantRest, _ := strings.Cut(want, " ")
	if rest != wantRest {
		t.Fatalf("got  %q\nwant %q", rest, wantRest)
	}
	if got[:len(logstamp.Format(now))] != logstamp.Format(now) {
		t.Fatalf("line does not start with the logstamp: %q", got)
	}
	got = formatStartLine(now, "x", "unstamped", "unknown", 1, "yolo-abc", "/bin/yolo", nil)
	if !strings.HasSuffix(got, "name=x build=unstamped commit=unknown pid=1 jail=yolo-abc exe=/bin/yolo") {
		t.Fatalf("plain values must stay bare: %q", got)
	}
	got = formatStartLine(now, "x", "b", "c", 1, "", "", os.ErrNotExist)
	if !strings.HasSuffix(got, "jail=- exe=unknown") {
		t.Fatalf("an unresolvable exe must read unknown: %q", got)
	}
}

func TestStartLineReadsTheJailFromTheEnvironment(t *testing.T) {
	t.Setenv(EnvJail, "yolo-ws-1")
	if !strings.Contains(StartLine(time.Now(), "n"), " jail=yolo-ws-1 ") {
		t.Fatal("StartLine must read " + EnvJail)
	}
	t.Setenv(EnvJail, "")
	if !strings.Contains(StartLine(time.Now(), "n"), " jail=- ") {
		t.Fatal("an empty " + EnvJail + " must read -")
	}
}
