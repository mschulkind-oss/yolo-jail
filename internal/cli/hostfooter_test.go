package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostClaudeStatus is a trimmed sample of the JSON Claude pipes to its status-line command.
const hostClaudeStatus = `{"model": {"id": "claude-opus-4", "display_name": "Opus"}}`

// hostFooterHome is a scratch home whose user config selects packs and profiles, with
// `yolo host apply --assert` already run into it, and returns the statusLine command that
// apply filled into ~/.claude/settings.json. The real $HOME is never read or written.
func hostFooterHome(t *testing.T, config string) (home, command string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), config)
	stubDeclaredBins(t)
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	return home, hostClaudeStatusCommand(t, home)
}

// hostClaudeStatusCommand is the statusLine command `yolo host apply` filled into home's
// ~/.claude/settings.json, failing the test when apply filled none of yolo's.
func hostClaudeStatusCommand(t *testing.T, home string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("host apply wrote no Claude settings: %v", err)
	}
	var settings struct {
		StatusLine struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("Claude settings do not parse: %v\n%s", err, raw)
	}
	if settings.StatusLine.Type != "command" || !strings.Contains(settings.StatusLine.Command, "yolo internal footer") {
		t.Fatalf("host apply filled statusLine = %+v, want yolo's footer command (build item 6)", settings.StatusLine)
	}
	return settings.StatusLine.Command
}

// hostFooterRun runs command as Claude runs a status-line command, through /bin/sh, with a
// `yolo` on PATH that only records its argv; then it hands that argv to this package's real
// front door (Main) in-process, at the host notch, with Claude's JSON on stdin. So the shell
// parses the command exactly as it will on a real host, and the arm under test is the one
// the binary dispatches to. It returns the footer line and anything written to stderr.
func hostFooterRun(t *testing.T, command string) (line, stderr string) {
	t.Helper()
	bin := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "argv")
	writeFile(t, filepath.Join(bin, "yolo"), "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$YOLO_FOOTER_ARGV\"\n")
	if err := os.Chmod(filepath.Join(bin, "yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := exec.Command("/bin/sh", "-c", command)
	sh.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "YOLO_FOOTER_ARGV=" + argsFile}
	if out, err := sh.CombinedOutput(); err != nil {
		t.Fatalf("status command failed under sh: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the status command never ran yolo: %v", err)
	}
	argv := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")

	// The host notch: no jail marker, and no table in the env, as for a bare host `claude`.
	for _, k := range []string{"YOLO_VERSION", "YOLO_USE_PROFILES", "YOLO_PROFILES", "YOLO_PROVIDERS",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"} {
		t.Setenv(k, "")
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inW.WriteString(hostClaudeStatus); err != nil {
		t.Fatal(err)
	}
	inW.Close()
	savedIn, savedOut, savedErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	code := Main(append([]string{"yolo"}, argv...))
	os.Stdin, os.Stdout, os.Stderr = savedIn, savedOut, savedErr
	outW.Close()
	errW.Close()
	out, _ := io.ReadAll(outR)
	errOut, _ := io.ReadAll(errR)
	if code != 0 {
		t.Errorf("yolo %q exited %d, want 0", argv, code)
	}
	return strings.TrimRight(string(out), "\n"), string(errOut)
}

// TestHostClaudeFooterNamesTheConfigsProfile is build item 6 of docs/design/agent-footer.md:
// `yolo host apply` fills host Claude's statusLine, and that command, run at the host, says
// `· host` and names the profile the user config selects (OQ-FT6: env first, else the user
// config's selection). It fails if the internal footer arm stops handing the renderer the host
// composition, since the host env carries no table and the footer would then name the login.
func TestHostClaudeFooterNamesTheConfigsProfile(t *testing.T) {
	cases := []struct {
		name, config, want string
	}{
		{"a pack-declared profile", `{"packs": ["claude"], "use_profiles": {"claude": "bedrock"}}`,
			"Opus · yolo: Bedrock · host"},
		// A profile only the user declares resolves through the user's `profiles`, the way
		// `yolo host env` resolves it, and is named because it differs from its provider.
		{"a user-declared profile", `{"packs": ["claude"], "profiles": {"work": {"provider": "bedrock"}}, ` +
			`"use_profiles": {"claude": "work"}}`,
			"Opus · yolo: Bedrock (profile work) · host"},
		{"no selection", `{"packs": ["claude"]}`,
			"Opus · yolo: Claude subscription · host"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, command := hostFooterHome(t, c.config)
			line, stderr := hostFooterRun(t, command)
			if line != c.want {
				t.Errorf("host Claude footer = %q, want %q", line, c.want)
			}
			if stderr != "" {
				t.Errorf("the footer wrote to stderr at the host: %q", stderr)
			}
		})
	}
}

// TestHostFooterStaysSilentOnABrokenPackSet: a pack the host cannot resolve right now (a git
// pack missing from the store) and a user config that does not parse both cost the footer its
// profile at most. Neither may put a word on stderr, which Claude would show.
func TestHostFooterStaysSilentOnABrokenPackSet(t *testing.T) {
	home, command := hostFooterHome(t, `{"packs": ["claude"], "use_profiles": {"claude": "bedrock"}}`)
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")

	writeFile(t, cfg, `{"packs": ["claude", {"source": "git+https://example.invalid/ghost.git", "name": "ghost"}], `+
		`"use_profiles": {"claude": "bedrock"}}`)
	if line, stderr := hostFooterRun(t, command); line != "Opus · yolo: Bedrock · host" || stderr != "" {
		t.Errorf("with an unresolvable pack: line=%q stderr=%q, want the profile and no stderr", line, stderr)
	}

	writeFile(t, cfg, `{"packs": [`)
	if line, stderr := hostFooterRun(t, command); line != "Opus · yolo: Claude subscription · host" || stderr != "" {
		t.Errorf("with a config that does not parse: line=%q stderr=%q, want the login and no stderr", line, stderr)
	}
}

// TestHostFooterChecksNoFetchedPackOut: the host profile read runs on every status-line refresh,
// so it may not write into the pack store (docs/design/agent-footer.md §2.1). A fetched pack
// whose tree is already checked out is read where it sits; one whose tree is gone is skipped,
// never checked out again, and the footer under-claims the profile only that pack declares.
func TestHostFooterChecksNoFetchedPackOut(t *testing.T) {
	repo := gitPackRepoWith(t, map[string]string{
		"pack.json": `{"name": "gp", "contributes": [{"kind": "profile", "name": "mybed", "provider": "bedrock"}]}`,
	})
	home := gitPackHome(t, "git+file://"+repo+"?ref=main", `,"use_profiles":{"claude":"mybed"}`)
	installGitPack(t)
	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	command := hostClaudeStatusCommand(t, home)

	if line, stderr := hostFooterRun(t, command); line != "Opus · yolo: Bedrock (profile mybed) · host" || stderr != "" {
		t.Errorf("with the pack's tree in the store: line=%q stderr=%q, want its profile named", line, stderr)
	}

	trees := filepath.Join(paths.PacksDir(), "trees")
	if err := os.RemoveAll(trees); err != nil {
		t.Fatal(err)
	}
	line, stderr := hostFooterRun(t, command)
	if entries, _ := os.ReadDir(trees); len(entries) != 0 {
		t.Errorf("a status-line refresh checked the fetched pack out again: %s holds %v", trees, entries)
	}
	if line != "Opus · yolo: mybed · host" || stderr != "" {
		t.Errorf("with the pack's tree gone: line=%q stderr=%q, want the bare profile name and no stderr", line, stderr)
	}
}

// hostFooterRun's argv capture has to survive an argument holding spaces and quotes, or the
// in-process run would be testing a different command from the one sh ran.
func TestHostFooterRunKeepsArgvWhole(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "argv")
	writeFile(t, filepath.Join(bin, "yolo"), "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$YOLO_FOOTER_ARGV\"\n")
	if err := os.Chmod(filepath.Join(bin, "yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := exec.Command("/bin/sh", "-c", `yolo internal footer --login 'Claude subscription' --template '{a} · b'`)
	sh.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "YOLO_FOOTER_ARGV=" + argsFile}
	if out, err := sh.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw, _ := os.ReadFile(argsFile)
	got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	want := []string{"internal", "footer", "--login", "Claude subscription", "--template", "{a} · b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("argv = %q, want %q", got, want)
	}
}
