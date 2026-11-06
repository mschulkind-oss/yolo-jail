package clipimage

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRun is a Runner that answers from a table keyed by the command line, and records
// every call, so no test reaches a real clipboard tool.
type fakeRun struct {
	answers map[string]fakeAnswer
	calls   []string
	stdins  [][]byte
}

type fakeAnswer struct {
	out []byte
	err error
}

func (f *fakeRun) run(ctx context.Context, limit int64, stdin []byte, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	f.stdins = append(f.stdins, stdin)
	a, ok := f.answers[line]
	if !ok {
		return nil, &ExitError{Code: 1, Stderr: "unexpected call " + line}
	}
	if limit > 0 && int64(len(a.out)) > limit {
		return nil, errTooLargeRead
	}
	return a.out, a.err
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func onPath(names ...string) func(string) (string, error) {
	return func(n string) (string, error) {
		for _, x := range names {
			if x == n {
				return "/stub/" + n, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestWaylandReaderPicksPNGOverJPEG(t *testing.T) {
	f := &fakeRun{answers: map[string]fakeAnswer{
		"wl-paste --list-types":                   {out: []byte("text/plain\nimage/jpeg\nimage/png\n")},
		"wl-paste --no-newline --type image/png":  {out: []byte("PNGBYTES")},
		"wl-paste --no-newline --type image/jpeg": {out: []byte("JPEGBYTES")},
	}}
	r := Reader{GOOS: "linux", Getenv: env(map[string]string{"WAYLAND_DISPLAY": "wayland-0"}), LookPath: onPath("wl-paste", "xclip"), Run: f.run}
	c, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != "image/png" || string(c.Data) != "PNGBYTES" || c.Tool != "wl-paste" {
		t.Fatalf("got %+v", c)
	}
}

func TestWaylandWithoutWlPasteFallsBackToXclip(t *testing.T) {
	f := &fakeRun{answers: map[string]fakeAnswer{
		"xclip -selection clipboard -t TARGETS -o":   {out: []byte("TARGETS\nimage/gif\nUTF8_STRING\n")},
		"xclip -selection clipboard -t image/gif -o": {out: []byte("GIFBYTES")},
	}}
	r := Reader{GOOS: "linux", Getenv: env(map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"}), LookPath: onPath("xclip"), Run: f.run}
	c, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != "image/gif" || c.Tool != "xclip" {
		t.Fatalf("got %+v", c)
	}
}

func TestReaderRefusals(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		path     []string
		answers  map[string]fakeAnswer
		want     error
		wantText string
	}{
		{"no display", map[string]string{}, []string{"wl-paste", "xclip"}, nil, ErrNoSession, "DISPLAY"},
		{"no tool", map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"}, nil, nil, ErrNoReader, "install wl-clipboard"},
		{"text only", map[string]string{"DISPLAY": ":0"}, []string{"xclip"},
			map[string]fakeAnswer{"xclip -selection clipboard -t TARGETS -o": {out: []byte("UTF8_STRING\nTEXT\n")}}, ErrNoImage, "copy an image"},
		{"empty clipboard", map[string]string{"WAYLAND_DISPLAY": "w"}, []string{"wl-paste"},
			map[string]fakeAnswer{"wl-paste --list-types": {err: &ExitError{Code: 1, Stderr: "Nothing is copied"}}}, ErrNoImage, "copy an image"},
		{"webp only", map[string]string{"WAYLAND_DISPLAY": "w"}, []string{"wl-paste"},
			map[string]fakeAnswer{"wl-paste --list-types": {out: []byte("image/webp\n")}}, ErrUnsupportedType, "as PNG"},
		{"too large", map[string]string{"WAYLAND_DISPLAY": "w"}, []string{"wl-paste"},
			map[string]fakeAnswer{
				"wl-paste --list-types":                  {out: []byte("image/png\n")},
				"wl-paste --no-newline --type image/png": {out: make([]byte, MaxBytes+1)},
			}, ErrTooLarge, "32 MiB"},
		{"owner does not answer", map[string]string{"WAYLAND_DISPLAY": "w"}, []string{"wl-paste"},
			map[string]fakeAnswer{
				"wl-paste --list-types":                  {out: []byte("image/png\n")},
				"wl-paste --no-newline --type image/png": {err: context.DeadlineExceeded},
			}, ErrDeadline, "copy the image again"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRun{answers: tc.answers}
			r := Reader{GOOS: "linux", Getenv: env(tc.env), LookPath: onPath(tc.path...), Run: f.run}
			_, err := r.Read(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("err %q does not name its next step (%q)", err, tc.wantText)
			}
		})
	}
}

func TestMacReaderDecodesTheAppleScriptData(t *testing.T) {
	want := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff}
	script := `the clipboard as «class PNGf»`
	f := &fakeRun{answers: map[string]fakeAnswer{
		"osascript -e " + script: {out: []byte("«data PNGf" + strings.ToUpper(hex.EncodeToString(want)) + "»\n")},
	}}
	r := Reader{GOOS: "darwin", Getenv: env(nil), LookPath: onPath("osascript"), Run: f.run}
	c, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c.Data, want) || c.Type != "image/png" || c.Tool != "osascript" {
		t.Fatalf("got %+v", c)
	}
}

func TestMacReaderWithNoImageNamesTheNextStep(t *testing.T) {
	script := `the clipboard as «class PNGf»`
	f := &fakeRun{answers: map[string]fakeAnswer{
		"osascript -e " + script: {err: &ExitError{Code: 1, Stderr: "execution error: Can’t make some data into the expected type. (-1700)"}},
	}}
	r := Reader{GOOS: "darwin", Getenv: env(nil), LookPath: onPath("osascript"), Run: f.run}
	if _, err := r.Read(context.Background()); !errors.Is(err, ErrNoImage) {
		t.Fatalf("err = %v, want ErrNoImage", err)
	}
}

func TestCopyTextPerHost(t *testing.T) {
	cases := []struct {
		goos string
		env  map[string]string
		path []string
		want string
	}{
		{"darwin", nil, []string{"pbcopy"}, "pbcopy"},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "w"}, []string{"wl-copy", "xclip"}, "wl-copy"},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"}, []string{"xclip"}, "xclip -selection clipboard -i"},
	}
	for _, tc := range cases {
		f := &fakeRun{answers: map[string]fakeAnswer{tc.want: {}}}
		r := Reader{GOOS: tc.goos, Getenv: env(tc.env), LookPath: onPath(tc.path...), Run: f.run}
		if err := r.CopyText(context.Background(), "/workspace/.yolo/paste/x.png"); err != nil {
			t.Fatalf("%s: %v", tc.want, err)
		}
		if len(f.calls) != 1 || f.calls[0] != tc.want || string(f.stdins[0]) != "/workspace/.yolo/paste/x.png" {
			t.Fatalf("calls %q stdin %q", f.calls, f.stdins)
		}
	}
}

// TestExecRunnerReadsAStubOnPath runs the real exec path against a stub script, so the
// plumbing (stdout limit, exit status, stderr capture) is exercised without any clipboard.
func TestExecRunnerReadsAStubOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nif [ \"$1\" = --list-types ]; then echo image/png; exit 0; fi\nprintf 'abcdef'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fail := filepath.Join(dir, "failing")
	if err := os.WriteFile(fail, []byte("#!/bin/sh\necho 'Nothing is copied' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := ExecRun(context.Background(), 100, nil, stub, "--no-newline")
	if err != nil || string(out) != "abcdef" {
		t.Fatalf("out %q err %v", out, err)
	}
	if _, err := ExecRun(context.Background(), 3, nil, stub, "x"); !errors.Is(err, errTooLargeRead) {
		t.Fatalf("over the limit: err = %v", err)
	}
	_, err = ExecRun(context.Background(), 100, nil, fail)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 1 || !strings.Contains(ee.Stderr, "Nothing is copied") {
		t.Fatalf("err = %#v", err)
	}
}
