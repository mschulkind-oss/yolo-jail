package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// THE CALL SITE of the `files` retire (entrypoint.RenderHostFiles): `yolo host apply --assert`,
// driven through applyHost into a scratch HOME, against a pack selected by path whose
// pack.json changes between applies — the dotfiles §25 sequence. A renamed file and a moved
// directory contribution leave the home; then a pack that declares no `files` at all has the
// rest of its old output retired too, which a render that only visits packs declaring the kind
// would never reach. A neighbouring file the user put in the load-all directory is untouched
// throughout.
func TestHostApplyRetiresFilesThePackStoppedShipping(t *testing.T) {
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "files", "a.ts"), "export const a = 1\n")
	writeFile(t, filepath.Join(local, "files", "b.ts"), "export const b = 1\n")
	writeFile(t, filepath.Join(local, "files", "board", "client.ts"), "client\n")
	declare := func(contribs string) {
		writeFile(t, filepath.Join(local, "pack.json"), `{"name":"lp","contributes":[`+contribs+`]}`)
	}
	declare(`{"kind":"files","from":"files/a.ts","into":".scratch/ext/a.ts"},` +
		`{"kind":"files","from":"files/board","into":".scratch/ext/old"}`)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Setenv("YOLO_VERSION", "")
	selectPacksWith(t, home, `{"source":"file://`+local+`","name":"lp"}`, `,"host_management":"own"`)
	t.Chdir(t.TempDir())

	ext := filepath.Join(home, ".scratch", "ext")
	user := filepath.Join(ext, "user.ts")
	writeFile(t, user, "mine\n")

	apply := func() string {
		t.Helper()
		var out, errw bytes.Buffer
		if rc := applyHost(&out, &errw, false, true, strings.NewReader("")); rc != 0 {
			t.Fatalf("host apply rc=%d\n%s%s", rc, out.String(), errw.String())
		}
		return out.String() + errw.String()
	}
	present := func(rel string, report string) {
		t.Helper()
		if _, err := os.Lstat(filepath.Join(ext, filepath.FromSlash(rel))); err != nil {
			t.Errorf("~/.scratch/ext/%s missing: %v\n%s", rel, err, report)
		}
	}
	gone := func(rel string, report string) {
		t.Helper()
		if _, err := os.Lstat(filepath.Join(ext, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("~/.scratch/ext/%s is still in the home (%v)\n%s", rel, err, report)
		}
	}

	report := apply()
	present("a.ts", report)
	present("old/client.ts", report)

	declare(`{"kind":"files","from":"files/b.ts","into":".scratch/ext/b.ts"},` +
		`{"kind":"files","from":"files/board","into":".scratch/ext/new"}`)
	report = apply()
	gone("a.ts", report)
	gone("old", report)
	present("b.ts", report)
	present("new/client.ts", report)
	if !strings.Contains(report, filepath.Join(ext, "a.ts")) {
		t.Errorf("the removal must be disclosed by path:\n%s", report)
	}

	declare(``)
	report = apply()
	gone("b.ts", report)
	gone("new", report)

	if got, err := os.ReadFile(user); err != nil || string(got) != "mine\n" {
		t.Errorf("the user's own file was touched: %q, %v", got, err)
	}
}

// filesRetireFixture is a scratch home selecting one by-path pack whose pack.json the test
// rewrites between applies, with the launch gate's opt-in on (gateFixture's environment).
func filesRetireFixture(t *testing.T) (ext string, declare func(string)) {
	t.Helper()
	local := t.TempDir()
	writeFile(t, filepath.Join(local, "files", "a.ts"), "export const a = 1\n")
	writeFile(t, filepath.Join(local, "files", "b.ts"), "export const b = 1\n")
	declare = func(contribs string) {
		writeFile(t, filepath.Join(local, "pack.json"), `{"name":"lp","contributes":[`+contribs+`]}`)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Setenv("YOLO_VERSION", "")
	selectPacksWith(t, home, `{"source":"file://`+local+`","name":"lp"}`,
		`,"host_management":"own","host_apply_on_launch":true`)
	setGateTTY(t, false)
	t.Chdir(t.TempDir())
	return filepath.Join(home, ".scratch", "ext"), declare
}

// THE LAUNCH GATE says what the retire did. Its apply's report is buffered, so before the gate
// printed the survey's retire lines, `yolo host -- <agent>` removed a file and said only
// "synchronized host configuration"; and a file left in place was said by nothing at all, the
// survey counting it in sync. Driven through hostApplyGate, the gate's own entry.
func TestHostApplyGateShowsTheFilesRetire(t *testing.T) {
	ext, declare := filesRetireFixture(t)
	declare(`{"kind":"files","from":"files/a.ts","into":".scratch/ext/a.ts"},` +
		`{"kind":"files","from":"files/b.ts","into":".scratch/ext/b.ts"}`)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}

	// The auto-apply path: a.ts is dropped and removed.
	declare(`{"kind":"files","from":"files/b.ts","into":".scratch/ext/b.ts"}`)
	var errw bytes.Buffer
	if !hostApplyGate(&errw, nil, "pi") {
		t.Fatalf("the gate refused:\n%s", errw.String())
	}
	if _, err := os.Lstat(filepath.Join(ext, "a.ts")); !os.IsNotExist(err) {
		t.Fatalf("the gate's apply did not retire a.ts (%v):\n%s", err, errw.String())
	}
	if want := "yolo host: lp/files ~/.scratch/ext/a.ts: removed"; !strings.Contains(errw.String(), want) {
		t.Errorf("the gate must disclose the removal (%q):\n%s", want, errw.String())
	}

	// The no-change path: b.ts is changed by the user, then dropped — left in place, and the
	// gate names it with its rm on every launch.
	b := filepath.Join(ext, "b.ts")
	if err := os.Chmod(b, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, b, "my edit\n")
	declare(``)
	for i := 0; i < 2; i++ {
		errw.Reset()
		if !hostApplyGate(&errw, nil, "pi") {
			t.Fatalf("the gate refused:\n%s", errw.String())
		}
		out := errw.String()
		for _, want := range []string{"~/.scratch/ext/b.ts: left in place", "rm " + shquote.Quote(b)} {
			if !strings.Contains(out, want) {
				t.Errorf("launch %d: the gate must name the left file (%q):\n%s", i+1, want, out)
			}
		}
	}
	if got, err := os.ReadFile(b); err != nil || string(got) != "my edit\n" {
		t.Errorf("the user's changed file was touched: %q, %v", got, err)
	}
}

// A pack with NO `files` left whose only remaining recorded path the user replaced with a link
// still reaches the retire, and its left-in-place line, through `yolo host apply`.
func TestHostApplyNamesAReplacedPathOfAPackWithNoFilesLeft(t *testing.T) {
	ext, declare := filesRetireFixture(t)
	declare(`{"kind":"files","from":"files/a.ts","into":".scratch/ext/a.ts"}`)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	a := filepath.Join(ext, "a.ts")
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", a); err != nil {
		t.Fatal(err)
	}
	declare(``)
	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	if !strings.Contains(report, "left in place") || !strings.Contains(report, a) {
		t.Errorf("the replaced path must be named as left in place:\n%s", report)
	}
	// One destination unrelated to the pack is in sync in this home whatever it holds (a pack
	// declaring nothing reports the same count), so a second would be the left path.
	if !strings.Contains(report, " 1 destination already in sync") {
		t.Errorf("a path left in place must not count as in sync:\n%s", report)
	}
	if !strings.Contains(report, "1 file a pack no longer ships is still in your home") {
		t.Errorf("the verdict must name what was left, not call the home up to date:\n%s", report)
	}
	if fi, err := os.Lstat(a); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the user's link was touched: %v", err)
	}
}

// A launch the gate refuses over a write-time failure in the program's own configuration still
// discloses what the apply's files retire already did: the files are gone and the record is
// saved, so this is the only launch that can say so.
func TestHostApplyGateRefusalStillShowsTheFilesRetire(t *testing.T) {
	ext, declare := filesRetireFixture(t)
	declare(`{"kind":"files","from":"files/a.ts","into":".scratch/ext/a.ts"},` +
		`{"kind":"files","from":"files/b.ts","into":".scratch/ext/b.ts"}`)
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("assert apply rc=%d\n%s", rc, report)
	}
	declare(`{"kind":"files","from":"files/b.ts","into":".scratch/ext/b.ts"}`)
	prev := hostApplyGateWrite
	hostApplyGateWrite = func(out, errw io.Writer, color, write bool, stdin io.Reader, s *hostApplySurvey) int {
		applyHostSurveyed(out, errw, color, write, stdin, s)
		s.failures = append(s.failures, hostFailure{What: "boom"})
		return 1
	}
	t.Cleanup(func() { hostApplyGateWrite = prev })
	var errw bytes.Buffer
	if hostApplyGate(&errw, nil, "pi") {
		t.Fatalf("the gate launched over a failure in pi's own configuration:\n%s", errw.String())
	}
	if _, err := os.Lstat(filepath.Join(ext, "a.ts")); !os.IsNotExist(err) {
		t.Fatalf("the gate's apply did not retire a.ts (%v):\n%s", err, errw.String())
	}
	if want := "yolo host: lp/files ~/.scratch/ext/a.ts: removed"; !strings.Contains(errw.String(), want) {
		t.Errorf("the refusal must still disclose the removal (%q):\n%s", want, errw.String())
	}
}
