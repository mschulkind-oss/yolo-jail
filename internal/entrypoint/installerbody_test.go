package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// A `via: installer` pack's launcher downloads the vendor's script and runs it with bash
// (nativeLauncherTemplate's _run_installer). The web-page case is covered in
// nativelauncher_test.go; these cells cover the other wrong body: bytes that are not a
// script at all.
//
// It happened. The Pack Installs workflow (.github/workflows/packs.yml) once went red
// because agy's vendor CDN, for a while, answered the installer URL with a BINARY instead
// of the install script. The launcher handed those bytes to bash, and the failure then read
// as bash's complaint about a file it could not run rather than as a wrong response from a
// named URL.
//
// The rule these cells pin, over the body's FIRST KiB (1024 bytes):
//
//   - a NUL byte anywhere in it means binary, and is refused whatever else the body says;
//   - otherwise a first line starting with "#!" is a script, and runs;
//   - otherwise the body must be TEXT, meaning no byte that file(1)'s text table
//     (text_chars in its src/encoding.c) marks as never appearing in text:
//     0x00-0x06, 0x0E-0x19, 0x1C-0x1F and 0x7F. Bytes 0x80 and above count as text, so
//     UTF-8 passes.

// probetoolInstaller is a working installer body: it lands an executable probetool where
// nativeLauncherTemplate's REAL_BIN looks and says so. first is its first line (a shebang,
// a comment, or deliberately bad bytes), and the body ends with "exit 0" so that anything
// appended after it (a payload, padding) is never executed as shell.
func probetoolInstaller(first string) string {
	return strings.Join([]string{
		first,
		`mkdir -p "$HOME/.local/bin"`,
		`printf '#!/bin/bash\necho PROBETOOL_RAN\n' > "$HOME/.local/bin/probetool"`,
		`chmod +x "$HOME/.local/bin/probetool"`,
		`echo INSTALLER_RAN`,
		`exit 0`,
	}, "\n") + "\n"
}

// withNULAt pads body with a comment line so that its first NUL byte sits at exactly offset
// off, then appends a short binary tail. The padding is a shell comment and the body already
// ended in "exit 0", so if the launcher runs the result anyway, bash installs probetool and
// stops before the tail.
func withNULAt(t *testing.T, body string, off int) string {
	t.Helper()
	if len(body)+2 > off {
		t.Fatalf("body is %d bytes; cannot put a NUL at offset %d", len(body), off)
	}
	padded := body + "#" + strings.Repeat("x", off-len(body)-2) + "\n"
	if len(padded) != off {
		t.Fatalf("padding arithmetic: %d bytes, want %d", len(padded), off)
	}
	return padded + "\x00\x00\x01payload"
}

// assertRefused checks the refusal's three observable parts: the invocation fails, the
// output names the URL and says what the bytes were (want), and the installer never ran.
func assertRefused(t *testing.T, url, want string, rc int, out string) {
	t.Helper()
	if rc == 0 {
		t.Errorf("a body that is not a script must not count as an install (rc=0)\n%s", out)
	}
	if !strings.Contains(out, "not a shell script") {
		t.Errorf("output must say the URL did not serve a shell script, got:\n%s", out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("output must say %q, got:\n%s", want, out)
	}
	if !strings.Contains(out, url) {
		t.Errorf("output must name the URL that misbehaved, got:\n%s", out)
	}
	// The body is a WORKING installer followed by the bad bytes, so either marker here
	// means bash was handed the body anyway, which is the bug.
	if strings.Contains(out, "INSTALLER_RAN") || strings.Contains(out, "PROBETOOL_RAN") {
		t.Errorf("the refused body reached bash and ran:\n%s", out)
	}
	if strings.Contains(out, "cannot execute binary file") {
		t.Errorf("a binary reached bash; the launcher must refuse it first:\n%s", out)
	}
}

// TestNativeLauncherRejectsABinary is the agy Pack Installs failure, reduced, in two shapes.
func TestNativeLauncherRejectsABinary(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			// What a CDN serving the wrong file looks like: an executable. An ELF header
			// has NULs from byte 7 on.
			name: "an ELF executable",
			body: "\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 56) + "\x02\x00\x3e\x00binary",
		},
		{
			// The shape bash does not stop on by itself: a runnable script whose first
			// two lines are clean, with a NUL inside the first KiB. bash's own
			// binary-file check looks only at the first line (two after a "#!"), so the
			// old launcher RAN this, and it installed.
			name: "a working script with a NUL in its first KiB",
			body: withNULAt(t, probetoolInstaller("#!/bin/bash"), 512),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := serveBody(t, 200, "application/octet-stream", tc.body)
			rc, out := runNativeLauncher(t, url)
			assertRefused(t, url, "binary", rc, out)
		})
	}
}

// TestNativeLauncherBinaryCheckCoversExactlyTheFirstKiB pins the window's edge, both sides.
// A NUL at offset 1023 (the KiB's last byte) is refused; one at offset 1024 is past the
// window and the script runs.
//
// The accepted side is not an arbitrary edge case. A self-extracting installer is a shell
// header with a binary payload after it, so "no NUL anywhere" would refuse a working shape.
// Only a header shorter than a KiB is refused, which is where a real installer never ends.
func TestNativeLauncherBinaryCheckCoversExactlyTheFirstKiB(t *testing.T) {
	head := probetoolInstaller("#!/bin/bash")

	t.Run("NUL at offset 1023 is refused", func(t *testing.T) {
		url := serveBody(t, 200, "application/x-sh", withNULAt(t, head, 1023))
		rc, out := runNativeLauncher(t, url)
		assertRefused(t, url, "binary", rc, out)
	})
	t.Run("NUL at offset 1024 runs", func(t *testing.T) {
		url := serveBody(t, 200, "application/x-sh", withNULAt(t, head, 1024))
		rc, out := runNativeLauncher(t, url)
		if rc != 0 {
			t.Errorf("a NUL past the first KiB must not refuse a working installer, rc=%d\n%s", rc, out)
		}
		if !strings.Contains(out, "PROBETOOL_RAN") {
			t.Errorf("the installer did not run, or the launcher did not exec its binary:\n%s", out)
		}
	})
}

// TestNativeLauncherRejectsNonTextWithoutAShebang covers the second clause: no NUL, no
// "#!" line, and bytes that do not occur in text. The body is otherwise a working installer,
// and the old launcher ran it: bash reported the first line as a command it could not find,
// then ran the rest.
func TestNativeLauncherRejectsNonTextWithoutAShebang(t *testing.T) {
	url := serveBody(t, 200, "application/octet-stream", probetoolInstaller("\x01\x02\x03\x1f\x7f"))
	rc, out := runNativeLauncher(t, url)
	assertRefused(t, url, "neither a #! script nor text", rc, out)
}

// TestNativeLauncherAcceptsScriptsWithUnusualBytes is the negative control for the two
// refusals above. Each cell is a body the rule must NOT refuse, so a check tightened past
// the rule (every control byte, anything non-ASCII) fails here.
func TestNativeLauncherAcceptsScriptsWithUnusualBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			// A "#!" line is enough, whatever control bytes follow it (short of a NUL).
			name: "a shebang script carrying non-text control bytes",
			body: probetoolInstaller("#!/bin/bash\n# raw bytes in a comment: \x01\x02\x7f"),
		},
		{
			// No shebang, but text by file(1)'s rule: UTF-8, a tab, ESC and SUB (both
			// text there), and a CR inside a comment.
			name: "a shebang-less script with UTF-8 and text control bytes",
			body: probetoolInstaller("# installer ✓ \t\x1b[32mgreen\x1b[0m \x1a end\r"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := serveBody(t, 200, "text/plain", tc.body)
			rc, out := runNativeLauncher(t, url)
			if rc != 0 {
				t.Errorf("this body is a script and must run, rc=%d\n%s", rc, out)
			}
			if !strings.Contains(out, "INSTALLER_RAN") || !strings.Contains(out, "PROBETOOL_RAN") {
				t.Errorf("the installer did not run, or the launcher did not exec its binary:\n%s", out)
			}
			if strings.Contains(out, "not a shell script") {
				t.Errorf("a script was refused:\n%s", out)
			}
		})
	}
}

// pathWithout returns a PATH of one directory holding a symlink to every executable the
// current PATH resolves, minus the names in drop. The first directory to offer a name wins,
// as it does in a PATH lookup, so the result behaves like the real PATH with those tools
// uninstalled.
func pathWithout(t *testing.T, drop ...string) string {
	t.Helper()
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	farm := t.TempDir()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if skip[name] {
				continue
			}
			src := filepath.Join(dir, name)
			fi, err := os.Stat(src)
			if err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
				continue
			}
			// An existing link means an earlier PATH directory already offered this name.
			if err := os.Symlink(src, filepath.Join(farm, name)); err != nil && !os.IsExist(err) {
				t.Fatal(err)
			}
		}
	}
	for _, d := range drop {
		if _, err := os.Lstat(filepath.Join(farm, d)); err == nil {
			t.Fatalf("%s is still on the test PATH", d)
		}
	}
	return farm
}

// TestNativeLauncherWithoutTrKeepsTheOlderChecks covers a host where the byte checks cannot
// run. tr is the one of their three tools whose absence would turn every verdict into
// "binary", so it is the one removed here. Two things must survive:
//
//   - a real "#!" installer still installs, so a missing tool never refuses a good body;
//   - a web page is still refused with its URL named, because that check is pure bash and
//     predates the byte checks.
//
// Both fail if the tool guard is deleted (the first cell), or if the web-page check is put
// back behind it (the second).
func TestNativeLauncherWithoutTrKeepsTheOlderChecks(t *testing.T) {
	noTr := "PATH=" + pathWithout(t, "tr")

	t.Run("a real #! installer still installs", func(t *testing.T) {
		// The launcher's exit status is NOT asserted: other steps of the launcher call tr
		// too, so without it the launch fails after the install. This cell pins the install
		// only, which is the part the body check decides.
		url := serveBody(t, 200, "application/x-sh", probetoolInstaller("#!/bin/bash"))
		_, out, home := runNativeLauncherWithEnv(t, url, noTr)
		if strings.Contains(out, "not a shell script") {
			t.Errorf("a missing tool refused a script:\n%s", out)
		}
		if !strings.Contains(out, "INSTALLER_RAN") {
			t.Errorf("the installer did not run:\n%s", out)
		}
		fi, err := os.Stat(filepath.Join(home, ".local", "bin", "probetool"))
		if err != nil || fi.Mode()&0o111 == 0 {
			t.Errorf("the installer ran but left no executable probetool (%v):\n%s", err, out)
		}
	})
	t.Run("a web page is still refused, with the URL named", func(t *testing.T) {
		url := serveBody(t, 200, "text/html; charset=utf-8",
			`<!doctype html><html lang="en-US"><head></head><body>moved</body></html>`)
		rc, out, _ := runNativeLauncherWithEnv(t, url, noTr)
		if rc == 0 {
			t.Errorf("a web page must not count as an install (rc=0)\n%s", out)
		}
		if !strings.Contains(out, "it served a web page") {
			t.Errorf("output must say the URL served a web page, got:\n%s", out)
		}
		if !strings.Contains(out, url) {
			t.Errorf("output must name the URL, got:\n%s", out)
		}
		if strings.Contains(out, "syntax error") {
			t.Errorf("HTML reached bash:\n%s", out)
		}
	})
}

// TestNativeLauncherClassifiesTheBodyBeforeRunningIt asserts the GENERATED launcher: the
// body check is present, and it sits between the download and the one line that hands the
// file to bash. The run cells above prove the behavior; this cell says where it lives, so a
// refactor that moved the bash call ahead of the check fails by name.
func TestNativeLauncherClassifiesTheBodyBeforeRunningIt(t *testing.T) {
	body := nativeAgentLauncher(
		&packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: "https://example.invalid/i.sh"},
		"/stamps", "/ws/.yolo/receipts.jsonl", "", true, launcherServers{},
		nil)
	download := strings.Index(body, `curl -fsSL "$URL" -o "$script"`)
	classify := strings.Index(body, `_installer_body_kind "$script"`)
	run := strings.Index(body, `bash "$script"`)
	if download < 0 || classify < 0 || run < 0 {
		t.Fatalf("launcher is missing a step (download=%d classify=%d run=%d):\n%s",
			download, classify, run, body)
	}
	if !(download < classify && classify < run) {
		t.Errorf("the body must be classified after the download and before bash runs it "+
			"(download=%d classify=%d run=%d)", download, classify, run)
	}
}
