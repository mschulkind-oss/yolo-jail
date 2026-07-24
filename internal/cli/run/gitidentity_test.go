package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	args := o.gitIdentityMountArgs("podman", wsState, map[string]struct{}{})

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
	args := o.gitIdentityMountArgs("podman", wsState, map[string]struct{}{})
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
	o1.gitIdentityMountArgs("podman", wsState, map[string]struct{}{})

	// Run 2 (same wsState): host has cleared email; name remains.
	o2 := &Options{Exec: fakeExec(map[string]ExecResult{
		"git config --get user.name": {Stdout: "Ada\n", Ran: true, RC: 0},
		// user.email probe misses → "" (cleared on host).
	})}
	o2.gitIdentityMountArgs("podman", wsState, map[string]struct{}{})

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
