package ghbroker

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The executor is tested against a FAKE gh that reports the environment, cwd and argv it
// received (docs/design/boundary-broker.md §11, testing constraints), which is how the
// scrub of §4.1 gets a test that fails when the scrub is deleted. Nothing here runs a real
// gh or reaches GitHub.

const fakeToken = "gho_FAKEfakeFAKEfake0123456789"

// fakeGH writes a gh stand-in into dir. It answers --version and `auth token`, records
// every other call's argv, cwd, environment and stdin under dir/calls, and then does what
// the argv's first word asks: `leak` prints the token split across two writes, `big`
// prints n bytes, `sleep` sleeps, `authfail` exits 4. A `--jq=env` or `--jq=$ENV` filter
// prints the environment, which is what gh's jq reads for those two (BB-D64). The broker's
// own `auth status --hostname github.com --active --json hosts` gets gh's JSON for a login
// whose token source is a host path, or, with a `nologin` file in dir, gh's answer for none.
func fakeGH(t *testing.T, dir, version string) string {
	t.Helper()
	calls := filepath.Join(dir, "calls")
	if err := os.MkdirAll(calls, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1" in
  --version) echo "gh version ` + version + ` (fake)"; exit 0 ;;
  auth) if [ "$2" = token ]; then echo "` + fakeToken + `"; exit 0; fi ;;
esac
d="` + calls + `/$$"
mkdir -p "$d"
printf '%s\n' "$@" > "$d/argv"
pwd > "$d/cwd"
env > "$d/env"
ls -A "$GH_CONFIG_DIR" > "$d/config-dir"
ls -A "$PWD" > "$d/cwd-listing"
cat > "$d/stdin"
case "$1" in
  leak) printf 'before gho_FAKEfake'; sleep 0.05; printf 'FAKEfake0123456789 after\n'; echo "err ` + fakeToken + `" >&2 ;;
  big) head -c "$2" /dev/zero | tr '\0' x ;;
  sleep) sleep "$2" ;;
  authfail) echo "To get started with GitHub CLI, please run:  gh auth login" >&2; exit 4 ;;
  auth) if [ "$*" = "auth status --hostname github.com --active --json hosts" ]; then
          if [ -e "` + dir + `/nologin" ]; then
            echo "You are not logged into any GitHub hosts. To log in, run: gh auth login" >&2
            echo '{"hosts":{}}'; exit 0
          fi
          echo '{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"me","tokenSource":"` + dir + `/run/1-x/config/hosts.yml","scopes":"gist, read:org, repo","gitProtocol":"https"}]}}'
          exit 0
        fi
        echo "ran $*" ;;
  *) case "$*" in
       *--repo=o/nologin*) echo "To get started with GitHub CLI, please run:  gh auth login" >&2; exit 4 ;;
       *--jq=env*|*--jq=\$ENV*) env; exit 0 ;;
     esac
     echo "ran $*" ;;
esac
`
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// oneCall returns the single recorded call's files.
func oneCall(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "calls"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("want exactly one recorded gh call, have %d (%v)", len(entries), err)
	}
	out := map[string]string{}
	base := filepath.Join(dir, "calls", entries[0].Name())
	files, _ := os.ReadDir(base)
	for _, f := range files {
		b, _ := os.ReadFile(filepath.Join(base, f.Name()))
		out[f.Name()] = string(b)
	}
	return out
}

type sinks struct {
	mu          sync.Mutex
	out, errOut bytes.Buffer
}

func (s *sinks) stdout(b []byte) { s.mu.Lock(); s.out.Write(b); s.mu.Unlock() }
func (s *sinks) stderr(b []byte) { s.mu.Lock(); s.errOut.Write(b); s.mu.Unlock() }

type runnerFixture struct {
	r       *Runner
	fakeDir string
	hostCfg string
}

func newRunnerFixture(t *testing.T, version string, opts func(*RunnerOptions)) runnerFixture {
	t.Helper()
	root := resolvedDir(t)
	fakeDir := filepath.Join(root, "fake")
	gh := fakeGH(t, fakeDir, version)
	hostCfg := filepath.Join(root, "host-gh-config")
	if err := os.MkdirAll(hostCfg, 0o700); err != nil {
		t.Fatal(err)
	}
	// The host's gh config: a login, and a config.yml carrying the hazards §4.1 names.
	_ = os.WriteFile(filepath.Join(hostCfg, "hosts.yml"), []byte("github.com:\n    user: me\n"), 0o600)
	_ = os.WriteFile(filepath.Join(hostCfg, "config.yml"),
		[]byte("aliases:\n    co: '!rm -rf /'\nbrowser: /evil\npager: /evil\n"), 0o600)
	env := map[string]string{
		"PATH":                     filepath.Dir(gh) + ":/usr/bin:/bin",
		"HOME":                     root,
		"GH_CONFIG_DIR":            hostCfg,
		"GH_TOKEN":                 "gho_from_the_environment",
		"GITHUB_TOKEN":             "ghp_from_the_environment",
		"GH_REPO":                  "evil/repo",
		"BROWSER":                  "/evil",
		"GH_BROWSER":               "/evil",
		"GH_PAGER":                 "/evil",
		"EDITOR":                   "/evil",
		"GIT_DIR":                  "/evil",
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus",
	}
	o := RunnerOptions{RunDir: filepath.Join(root, "run"), Getenv: func(k string) string { return env[k] }}
	if opts != nil {
		opts(&o)
	}
	r, err := NewRunner(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return runnerFixture{r: r, fakeDir: fakeDir, hostCfg: hostCfg}
}

func resolvedDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRunnerRunsGHUnderConditionsItOwns(t *testing.T) {
	f := newRunnerFixture(t, "2.101.0", nil)
	if f.r.Version != "2.101.0" || !f.r.TokenRead {
		t.Fatalf("runner %+v", f.r)
	}
	var s sinks
	res := f.r.Run([]string{"pr", "view", "--repo=o/r", "32"}, nil, s.stdout, s.stderr)
	if res.Exit != 0 || s.out.String() != "ran pr view --repo=o/r 32\n" {
		t.Fatalf("exit %d stdout %q stderr %q", res.Exit, s.out.String(), s.errOut.String())
	}
	call := oneCall(t, f.fakeDir)

	if got := call["argv"]; got != "pr\nview\n--repo=o/r\n32\n" {
		t.Errorf("gh received argv %q, want the canonical argv verbatim", got)
	}
	// An empty, broker-owned cwd — never the workspace (§9.5).
	if cwd := strings.TrimSpace(call["cwd"]); cwd != filepath.Join(f.r.runDir, "cwd") {
		t.Errorf("cwd %q", cwd)
	}
	if strings.TrimSpace(call["cwd-listing"]) != "" {
		t.Errorf("the cwd is not empty: %q", call["cwd-listing"])
	}
	// Only hosts.yml, and no config.yml: no aliases, browser or pager (H3, H5).
	if strings.TrimSpace(call["config-dir"]) != "hosts.yml" {
		t.Errorf("GH_CONFIG_DIR holds %q, want hosts.yml alone", call["config-dir"])
	}

	env := map[string]string{}
	for _, line := range strings.Split(call["env"], "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GH_REPO", "BROWSER",
		"GH_BROWSER", "EDITOR", "VISUAL", "GH_EDITOR", "GIT_DIR", "PAGER"} {
		if v, ok := env[k]; ok {
			t.Errorf("gh's environment carries %s=%q", k, v)
		}
	}
	want := map[string]string{
		"GH_HOST":                  "github.com",
		"GH_PROMPT_DISABLED":       "1",
		"GH_NO_UPDATE_NOTIFIER":    "1",
		"GH_PAGER":                 "cat",
		"NO_COLOR":                 "1",
		"GH_TELEMETRY":             "0",
		"GH_CONFIG_DIR":            filepath.Join(f.r.runDir, "config"),
		"XDG_DATA_HOME":            filepath.Join(f.r.runDir, "data"),
		"XDG_CACHE_HOME":           filepath.Join(f.r.runDir, "cache"),
		"XDG_STATE_HOME":           filepath.Join(f.r.runDir, "state"),
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("gh's %s = %q, want %q", k, env[k], v)
		}
	}
	var keys []string
	for k := range env {
		if strings.HasPrefix(k, "GIT_") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		t.Errorf("GIT_* variables reached gh: %v", keys)
	}
}

// BB-D64: `--jq env`, `--jq $ENV` and the formatting --template run on the host, and a jq
// filter reads the whole environment of the gh evaluating it (MEASURED against gh 2.101.0:
// `env` printed exactly the broker's variables, plus TCELL_MINIMIZE, which gh sets itself).
// So that environment is a set reviewed name by name, not a set some names are kept out of:
// a variable added here crosses to any jail that asks for `env`, and this test fails until
// someone has looked at it.
func TestTheEnvironmentAJqFilterCanReadIsExactlyTheReviewedSet(t *testing.T) {
	f := newRunnerFixture(t, "2.101.0", nil)
	var s sinks
	f.r.Run([]string{"pr", "view", "--repo=o/r", "1"}, nil, s.stdout, s.stderr)
	call := oneCall(t, f.fakeDir)
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(call["env"]), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "PWD", "OLDPWD", "SHLVL", "_":
			continue // the fake's own shell sets these; gh is not a shell
		}
		got = append(got, k)
		for _, secret := range []string{fakeToken, "gho_from_the_environment", "ghp_from_the_environment"} {
			if strings.Contains(v, secret) {
				t.Errorf("gh's %s carries a token: %q", k, v)
			}
		}
	}
	sort.Strings(got)
	want := []string{
		"DBUS_SESSION_BUS_ADDRESS", "DO_NOT_TRACK", "GH_CONFIG_DIR", "GH_HOST",
		"GH_NO_EXTENSION_UPDATE_NOTIFIER", "GH_NO_UPDATE_NOTIFIER", "GH_PAGER", "GH_PROMPT_DISABLED",
		"GH_SPINNER_DISABLED", "GH_TELEMETRY", "HOME", "NO_COLOR", "PATH", "TMPDIR",
		"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("gh's environment is %v, want exactly %v: a jail's `--jq env` prints every one, "+
			"so a new variable is reviewed for what it reveals before it is added (BB-D64)", got, want)
	}
}

// A dotfile manager's symlinked hosts.yml is the user's own and is followed.
func TestRunnerFollowsASymlinkedHostsFile(t *testing.T) {
	root := resolvedDir(t)
	gh := fakeGH(t, filepath.Join(root, "fake"), "2.101.0")
	real := filepath.Join(root, "dotfiles", "hosts.yml")
	if err := os.MkdirAll(filepath.Dir(real), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("github.com:\n    user: me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "cfg")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(cfg, "hosts.yml")); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": filepath.Dir(gh) + ":/usr/bin:/bin", "HOME": root, "GH_CONFIG_DIR": cfg}
	r, err := NewRunner(RunnerOptions{RunDir: filepath.Join(root, "run"), Getenv: func(k string) string { return env[k] }})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := os.ReadFile(filepath.Join(r.runDir, "config", "hosts.yml"))
	if err != nil || string(got) != "github.com:\n    user: me\n" {
		t.Fatalf("hosts.yml copy %q, %v", got, err)
	}
}

func TestRunnerFeedsStdinOnlyWhenGiven(t *testing.T) {
	f := newRunnerFixture(t, "2.101.0", nil)
	var s sinks
	f.r.Run([]string{"pr", "comment", "--body-file=-", "1"}, []byte("hello from the jail"), s.stdout, s.stderr)
	if got := oneCall(t, f.fakeDir)["stdin"]; got != "hello from the jail" {
		t.Fatalf("stdin %q", got)
	}
}

// BB-D16: the token is redacted from output even split across two writes, and counted.
func TestRunnerRedactsTheHostToken(t *testing.T) {
	f := newRunnerFixture(t, "2.101.0", nil)
	var s sinks
	res := f.r.Run([]string{"leak"}, nil, s.stdout, s.stderr)
	if strings.Contains(s.out.String()+s.errOut.String(), fakeToken) {
		t.Fatalf("the token crossed: stdout %q stderr %q", s.out.String(), s.errOut.String())
	}
	if s.out.String() != "before "+RedactionMarker+" after\n" {
		t.Fatalf("stdout %q", s.out.String())
	}
	if res.Redactions != 2 {
		t.Fatalf("redactions %d, want 2 (one per stream)", res.Redactions)
	}
}

func TestRunnerCutsOutputAtTheCap(t *testing.T) {
	f := newRunnerFixture(t, "2.101.0", func(o *RunnerOptions) { o.OutCap = 1000 })
	var s sinks
	res := f.r.Run([]string{"big", "5000"}, nil, s.stdout, s.stderr)
	if !res.Truncated || s.out.Len() != 1000 || res.BytesOut != 1000 {
		t.Fatalf("truncated=%v stdout %d bytes, bytes_out %d", res.Truncated, s.out.Len(), res.BytesOut)
	}
	if !strings.Contains(s.errOut.String(), "output cut at 1000 bytes") {
		t.Fatalf("stderr %q", s.errOut.String())
	}
	if res.Exit != 0 {
		t.Fatalf("gh's own exit code must still cross after the cut: %d", res.Exit)
	}
}

func TestRunnerStopsAtTheTimeout(t *testing.T) {
	f := newRunnerFixture(t, "2.101.0", func(o *RunnerOptions) { o.Timeout = 200 * time.Millisecond })
	var s sinks
	start := time.Now()
	res := f.r.Run([]string{"sleep", "30"}, nil, s.stdout, s.stderr)
	if res.Exit != ExitTimeout || !res.TimedOut || time.Since(start) > 10*time.Second {
		t.Fatalf("exit %d timedOut %v after %s", res.Exit, res.TimedOut, time.Since(start))
	}
}

// §5.1 rule 6, revised 2026-10-09: any host gh version is recorded and described, never
// judged against a range.
func TestRunnerRecordsAnyGHVersion(t *testing.T) {
	f := newRunnerFixture(t, "2.102.0", nil)
	if f.r.Version != "2.102.0" || !strings.Contains(f.r.Describe(), "version 2.102.0;") ||
		strings.Contains(f.r.Describe(), "tested") || strings.Contains(f.r.Summary(), "tested") {
		t.Fatalf("runner %+v: %s / %s", f.r, f.r.Describe(), f.r.Summary())
	}
}

func TestNewRunnerWithNoGH(t *testing.T) {
	_, err := NewRunner(RunnerOptions{RunDir: t.TempDir(), Getenv: func(k string) string {
		if k == "PATH" {
			return t.TempDir()
		}
		return ""
	}})
	if err != ErrNoGH {
		t.Fatalf("err %v, want ErrNoGH", err)
	}
}

func TestRedactorStreams(t *testing.T) {
	var got bytes.Buffer
	r := newRedactor("SECRET", func(b []byte) { got.Write(b) })
	for _, chunk := range []string{"aSE", "CR", "ETbSECRETc", "SEC"} {
		r.write([]byte(chunk))
	}
	r.flush()
	want := "a" + RedactionMarker + "b" + RedactionMarker + "cSEC"
	if got.String() != want || r.count != 2 {
		t.Fatalf("got %q (%d), want %q", got.String(), r.count, want)
	}
	var plain bytes.Buffer
	n := newRedactor("", func(b []byte) { plain.Write(b) })
	n.write([]byte("no token held"))
	n.flush()
	if plain.String() != "no token held" {
		t.Fatalf("an empty token must pass output through: %q", plain.String())
	}
}
