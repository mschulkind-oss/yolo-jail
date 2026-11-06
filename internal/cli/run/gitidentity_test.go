package run

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestComposeGitconfig pins the rendered INI: [user] with name/email (each
// omitted when empty) and [core] excludesFile when the gitignore is present.
func TestComposeGitconfig(t *testing.T) {
	got := composeGitconfig("Ada Lovelace", "ada@example.com", "/home/agent/.config/git/ignore")
	for _, want := range []string{
		"[user]\n",
		"\tname = Ada Lovelace\n",
		"\temail = ada@example.com\n",
		"[core]\n",
		"\texcludesFile = /home/agent/.config/git/ignore\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("composed config missing %q:\n%s", want, got)
		}
	}
}

// TestComposeGitconfigOmitsEmpty: an empty email drops the email line but keeps
// name; no gitignore drops the [core] section entirely.
func TestComposeGitconfigOmitsEmpty(t *testing.T) {
	got := composeGitconfig("Ada", "", "")
	if !strings.Contains(got, "\tname = Ada\n") {
		t.Errorf("want name line, got:\n%s", got)
	}
	if strings.Contains(got, "email") {
		t.Errorf("empty email must not emit an email line:\n%s", got)
	}
	if strings.Contains(got, "[core]") || strings.Contains(got, "excludesFile") {
		t.Errorf("absent gitignore must not emit [core]/excludesFile:\n%s", got)
	}
}

// TestGitConfigValueQuoting: ordinary values stay bare; INI-special chars and
// edge whitespace force quoting with escapes, matching what `git config` writes.
func TestGitConfigValueQuoting(t *testing.T) {
	cases := map[string]string{
		"Ada Lovelace":    "Ada Lovelace",      // spaces are fine bare
		"ada@example.com": "ada@example.com",   // @ is fine bare
		" leading":        `" leading"`,        // edge whitespace → quote
		"has # hash":      `"has # hash"`,      // comment char → quote
		`quote " inside`:  `"quote \" inside"`, // quote → escaped + quoted
		`back\slash`:      `"back\\slash"`,     // backslash → escaped + quoted
	}
	for in, want := range cases {
		if got := gitConfigValue(in); got != want {
			t.Errorf("gitConfigValue(%q) = %q, want %q", in, got, want)
		}
	}
}

// gitIdentityEnv builds Options whose Exec seam answers the host git-config
// probes from the given map (key = joined argv), plus a temp wsState dir.
func gitIdentityTestOpts(t *testing.T, probes map[string]string) (*Options, string) {
	t.Helper()
	cases := map[string]ExecResult{}
	for k, v := range probes {
		cases[k] = ExecResult{Stdout: v, Ran: true, RC: 0}
	}
	o := &Options{Exec: fakeExec(cases)}
	wsState := t.TempDir()
	return o, wsState
}

func TestAssembleGitIdentityWriteFailureDoesNotEmitMount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		rt       string
		rel      string
		mountSrc string
	}{
		{rt: "podman", rel: "yolo-gitconfig", mountSrc: "yolo-gitconfig"},
		{rt: "container", rel: filepath.Join(".config", "git", "config")},
	} {
		t.Run(tc.rt, func(t *testing.T) {
			wsState := t.TempDir()
			target := filepath.Join(wsState, tc.rel)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			// writeFileBeneath safely replaces an empty non-regular target. Keep a
			// child here so removing this conflicting directory fails deterministically.
			if err := os.WriteFile(filepath.Join(target, "keep"), []byte("not empty"), 0o600); err != nil {
				t.Fatal(err)
			}
			o := goldenOptions("/ws", t.TempDir())
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				switch strings.Join(argv, " ") {
				case "git config --get user.name":
					return ExecResult{Stdout: "Ada Lovelace\n", Ran: true, RC: 0}
				case "git config --get user.email":
					return ExecResult{Stdout: "ada@example.com\n", Ran: true, RC: 0}
				default:
					return ExecResult{Ran: false}
				}
			}
			in := relocationInput(t, tc.rt, wsState, nil)
			argv := o.assembleRunCmd(in)

			if in.assemblyErr == nil {
				t.Fatal("assembly did not propagate the failed Git identity write")
			}
			for i := 0; i+1 < len(argv); i++ {
				failedGitConfigMount := tc.mountSrc != "" && argv[i] == "-v" &&
					strings.HasPrefix(argv[i+1], filepath.Join(wsState, tc.mountSrc)+":")
				if failedGitConfigMount {
					t.Fatalf("assembly emitted a mount for the failed gitconfig write: %v", argv)
				}
			}
			for _, want := range []string{target, "writable", "not a directory", "rerun `yolo`"} {
				if !strings.Contains(in.assemblyErr.Error(), want) {
					t.Errorf("assembly error %q does not name %q", in.assemblyErr, want)
				}
			}
			if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
				t.Errorf("the conflicting directory must remain untouched: stat=%v err=%v", fi, err)
			}
			if data, err := os.ReadFile(filepath.Join(target, "keep")); err != nil || string(data) != "not empty" {
				t.Errorf("the existing child must remain untouched: data=%q err=%v", data, err)
			}
		})
	}
}

// TestRunRefusesFailedGitIdentityStaging pins the production caller of argv
// assembly: an identity the host supplied cannot silently disappear when the
// overlay's staging path is a directory. The fake keeper spawner is an assertion
// boundary; reaching it means Run tried to start a backend, but it cannot start
// a service or invoke a runtime.
func TestRunRefusesFailedGitIdentityStaging(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			home := packHome(t)
			writeUserPacks(t, home, `[]`)
			ws := t.TempDir()
			cname := yoloruntime.FromWorkspace(ws)
			wsState := runtimeHome(ws, rt)
			staged := "yolo-gitconfig"
			if rt == "container" {
				staged = filepath.Join(".config", "git", "config")
			}
			target := filepath.Join(wsState, staged)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			const sentinel = "existing data stays intact"
			if err := os.WriteFile(filepath.Join(target, "keep"), []byte(sentinel), 0o600); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, rt, &stdout, &stderr, nil)
			o.AcceptConfigChanges = true
			o.NeverAttach = true
			if rt == "container" {
				o.IsMacOS, o.IsLinux = true, false
			}
			repo, _ := o.RepoRoot()
			o.PathExists = func(p string) bool {
				return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
			}
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				switch strings.Join(append([]string{filepath.Base(argv[0])}, argv[1:]...), " ") { // the probe runs the path LookPath found
				case "git config --get user.name":
					return ExecResult{Stdout: "Ada Lovelace\n", Ran: true, RC: 0}
				case "git config --get user.email":
					return ExecResult{Stdout: "ada@example.com\n", Ran: true, RC: 0}
				case "container --version":
					return ExecResult{Stdout: "Apple container CLI version 1.1.0", Ran: true, RC: 0}
				case "container system status":
					return ExecResult{Stdout: "running", Ran: true, RC: 0}
				default:
					return ExecResult{Ran: true, RC: 0}
				}
			}
			var starts int
			previousSpawner := defaultKeeperSpawner
			defaultKeeperSpawner = func(*Options, string, *os.File, *os.File, *os.File, []*os.File) (func() int, error) {
				starts++
				return nil, errors.New("unexpected backend start in git identity refusal test")
			}
			t.Cleanup(func() { defaultKeeperSpawner = previousSpawner })
			o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
				return image.LoadResult{OK: true, Ref: goldenImageRef}
			}

			rc := Run(*o)
			output := stdout.String() + stderr.String()
			if rc == 0 {
				t.Errorf("Run() = 0, want refusal for failed identity staging:\n%s", output)
			}
			for _, want := range []string{target, "writable", "not a directory", "rerun `yolo`"} {
				if !strings.Contains(output, want) {
					t.Errorf("Run output does not name %q:\n%s", want, output)
				}
			}
			if starts != 0 {
				t.Errorf("Run attempted %d backend start(s) after the staging failure", starts)
			}
			if !launchLockFree(t, launchLockPath(cname)) {
				t.Error("Run did not release the workspace launch lock after refusing")
			}
			if rt == "podman" {
				entries, err := os.ReadDir(paths.HomeSkeletonRoot(cname))
				if err != nil || len(entries) != 0 {
					t.Errorf("Run did not clean up the unheld home skeleton: entries=%v err=%v", entries, err)
				}
			}
			if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
				t.Errorf("the conflicting directory was changed: stat=%v err=%v", fi, err)
			}
			if got, err := os.ReadFile(filepath.Join(target, "keep")); err != nil || string(got) != sentinel {
				t.Errorf("the conflicting directory's child changed: got=%q err=%v", got, err)
			}
		})
	}
}

// TestGitIdentityMountComposesAndMounts: with a host name+email, the composed
// gitconfig is written into wsState and mounted :ro at the jail path.
func TestGitIdentityMountComposesAndMounts(t *testing.T) {
	// No gitignore for this case: point HOME at an empty dir so the default
	// ~/.config/git/ignore does not resolve to a real file.
	t.Setenv("HOME", t.TempDir())
	o, wsState := gitIdentityTestOpts(t, map[string]string{
		"git config --get user.name":  "Ada Lovelace\n",
		"git config --get user.email": "ada@example.com\n",
	})
	args, err := o.gitIdentityMountArgs("podman", wsState, map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}

	staged := filepath.Join(wsState, "yolo-gitconfig")
	data, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("composed gitconfig not written: %v", err)
	}
	if !strings.Contains(string(data), "ada@example.com") {
		t.Errorf("staged config missing email:\n%s", data)
	}
	wantMount := staged + ":/home/agent/.config/git/config:ro"
	if !inStrSlice(args, wantMount) {
		t.Errorf("missing :ro gitconfig mount %q in %v", wantMount, args)
	}
}

// TestGitIdentityMountEmptyEmitsNothing: no identity and no gitignore → no args
// and no file (preserving the identity-less golden argv).
func TestGitIdentityMountEmptyEmitsNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o, wsState := gitIdentityTestOpts(t, map[string]string{}) // all probes miss → ""
	args, err := o.gitIdentityMountArgs("podman", wsState, map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 0 {
		t.Errorf("bare jail must emit no git-identity args, got %v", args)
	}
	if _, err := os.Stat(filepath.Join(wsState, "yolo-gitconfig")); !os.IsNotExist(err) {
		t.Errorf("bare jail must not write a gitconfig, stat err=%v", err)
	}
}

// TestGitIdentityMountStaleClearedEmail is the whole point of the port: when the
// host CLEARS user.email between runs, the recomposed file simply has no email
// line — the old add-only setter could never remove it.
func TestGitIdentityMountStaleClearedEmail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Run 1: host has name + email.
	o1, wsState := gitIdentityTestOpts(t, map[string]string{
		"git config --get user.name":  "Ada\n",
		"git config --get user.email": "ada@example.com\n",
	})
	if _, err := o1.gitIdentityMountArgs("podman", wsState, map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}

	// Run 2 (same wsState): host has cleared email; name remains.
	o2 := &Options{Exec: fakeExec(map[string]ExecResult{
		"git config --get user.name": {Stdout: "Ada\n", Ran: true, RC: 0},
		// user.email probe misses → "" (cleared on host).
	})}
	if _, err := o2.gitIdentityMountArgs("podman", wsState, map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(wsState, "yolo-gitconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "email") || strings.Contains(string(data), "example.com") {
		t.Errorf("cleared host email must vanish from the recomposed config:\n%s", data)
	}
	if !strings.Contains(string(data), "\tname = Ada\n") {
		t.Errorf("surviving name must remain:\n%s", data)
	}
}

// A5: ~/.gitconfig was a DECOY — the symlink resolves into a :ro bind, so
// `git config --global user.email x` failed with a bare "could not write config
// file /home/agent/.gitconfig: Device or resource busy", naming a path that looks
// writable and explaining nothing. The composed file said "edits do not persist",
// but a user never reads it: they hit the error through the alias.
//
// The composed config now includes a WRITABLE sibling and says so in its header,
// so the file the user lands on tells them where to write. The include must come
// FIRST: git applies includes in file order with last-definition-wins, so yolo's
// [user] identity has to be emitted AFTER it to stay authoritative.
func TestComposeGitconfigIncludesWritableSiblingFirst(t *testing.T) {
	got := composeGitconfig("Ada", "ada@example.com", "/home/agent/.config/git/ignore")

	if !strings.Contains(got, gitLocalConfigInJail) {
		t.Errorf("composed gitconfig does not include the writable sibling:\n%s", got)
	}
	includeAt := strings.Index(got, "[include]")
	userAt := strings.Index(got, "[user]")
	if includeAt < 0 || userAt < 0 {
		t.Fatalf("missing [include] or [user]:\n%s", got)
	}
	if includeAt > userAt {
		t.Errorf("[include] must precede [user] so yolo's identity wins (last-wins):\n%s", got)
	}
	// And the header must point the user at the writable file.
	if !strings.Contains(got, "git config --global") {
		t.Errorf("header does not tell the user where their writes go:\n%s", got)
	}
}
