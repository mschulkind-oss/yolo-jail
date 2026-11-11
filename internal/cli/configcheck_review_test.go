package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestConfigCheckRejectsMalformedProjectLocalAndIncludedFiles(t *testing.T) {
	for _, format := range []configdoc.Format{configdoc.TOML, configdoc.JSONC} {
		for _, kind := range []string{"project", "local", "project include", "local include"} {
			t.Run(string(format)+"/"+kind, func(t *testing.T) {
				good := "[machine.resources]\ncpus = 2\n"
				bad := "[machine.resources\ncpus = 2\n"
				file := "broken.toml"
				if format == configdoc.JSONC {
					good = `{"machine":{"resources":{"cpus":2}}}`
					bad = `{"machine":`
					file = "broken.jsonc"
				}
				workspace := testconfig.WorkspaceFormat(t, format, good)
				if kind == "project" {
					workspace = testconfig.WorkspaceFormat(t, format, bad)
				}
				localName := "yolo-jail.local.toml"
				if format == configdoc.JSONC {
					localName = "yolo-jail.local.jsonc"
				}
				if kind == "local" {
					if err := os.WriteFile(filepath.Join(workspace.Root, localName), []byte(bad), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if strings.Contains(kind, "include") {
					include := "include_if_found = [\"" + file + "\"]\n"
					if format == configdoc.JSONC {
						include = `{"include_if_found":["` + file + `"]}`
					}
					if kind == "project include" {
						workspace = testconfig.WorkspaceFormat(t, format, include)
					} else if err := os.WriteFile(filepath.Join(workspace.Root, localName), []byte(include), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(workspace.Root, file), []byte(bad), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				user := testconfig.UserFormat(t, configdoc.JSONC, `{}`)
				t.Setenv("HOME", user.Root)
				t.Setenv("YOLO_VERSION", "")
				withConfigEditCWD(t, workspace.Root)
				var out bytes.Buffer
				rc := configRunW([]string{"check"}, &out, &bytes.Buffer{})
				if rc != 1 || !strings.Contains(out.String(), "[FAIL]") || strings.Contains(out.String(), "[PASS] Merged config is semantically valid") {
					t.Fatalf("malformed %s config was not a hard dispatch failure (rc=%d):\n%s", kind, rc, out.String())
				}
				// The failure's next step re-runs the config-only verb, not the broad check.
				if !strings.Contains(out.String(), "then: yolo config check") || strings.Contains(out.String(), "then: yolo check") {
					t.Fatalf("malformed %s config's next step does not re-run yolo config check:\n%s", kind, out.String())
				}
			})
		}
	}
}

func TestConfigCheckRunsSharedConfigDeterminedGates(t *testing.T) {
	cases := []struct {
		name, user, want string
	}{
		{"required capability", `{"required_capabilities":["web_search"]}`, "config.required_capabilities declares 'web_search'"},
		{"undeclared pi profile", `{"packs":["pi"],"profile":{"pi":"groq"}}`, `profile "groq" selected for pi`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
			user := testconfig.UserFormat(t, configdoc.JSONC, tc.user)
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			withConfigEditCWD(t, workspace.Root)
			var out bytes.Buffer
			rc := configRunW([]string{"check"}, &out, &bytes.Buffer{})
			if rc != 1 || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("%s config gate missing from public dispatch (rc=%d):\n%s", tc.name, rc, out.String())
			}
		})
	}

	t.Run("selected program providers", func(t *testing.T) {
		pack := func(name string) string {
			root := filepath.Join(t.TempDir(), name)
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := `{"name":"` + name + `","contributes":[{"kind":"program","bin":"xtool","via":"npm","package":"xtool-pkg"}]}`
			if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			return "file://" + root
		}
		workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
		user := testconfig.UserFormat(t, configdoc.JSONC, `{"packs":["`+pack("provb")+`","`+pack("provc")+`"]}`)
		t.Setenv("HOME", user.Root)
		t.Setenv("YOLO_VERSION", "")
		withConfigEditCWD(t, workspace.Root)
		var out bytes.Buffer
		rc := configRunW([]string{"check"}, &out, &bytes.Buffer{})
		if rc != 1 || !strings.Contains(out.String(), "program xtool has more than one provider") || !strings.Contains(out.String(), "packs provb, provc") || strings.Contains(out.String(), "[PASS] Merged config is semantically valid") {
			t.Fatalf("selected program collision not reported through public dispatch (rc=%d):\n%s", rc, out.String())
		}
	})
}

func TestConfigCheckDoesNotFailAnUnprovenCapabilityGapWithUnavailablePack(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
	user := testconfig.UserFormat(t, configdoc.JSONC, `{"packs":["git+https://example.invalid/acme/packs//tool?ref=v1"],"required_capabilities":["web_search"]}`)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	var out bytes.Buffer
	rc := configRunW([]string{"check"}, &out, &bytes.Buffer{})
	if rc != 0 || !strings.Contains(out.String(), "Could not predict the capability gate") || !strings.Contains(out.String(), "[SKIP] Selected pack tool") || strings.Contains(out.String(), "[FAIL]") {
		t.Fatalf("incomplete selection was treated as proof that capability is absent (rc=%d):\n%s", rc, out.String())
	}
}

func TestConfigCheckValidatesFilteredReadableLoopholesBesideUnavailablePack(t *testing.T) {
	root := filepath.Join(t.TempDir(), "local-auth")
	module := filepath.Join(root, "loopholes", "local-auth")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.toml"), []byte("name = \"local-auth\"\n[[loophole]]\nfrom = \"loopholes/local-auth\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("filtered harmless file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"local-auth","settings":{"profile":{"type":"string","scope":"user","default":"safe"}}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	userText := `{"packs":[{"source":"file://` + root + `","exclude":["README.md"]},"git+https://example.invalid/acme/packs//missing?ref=v1"],"loopholes":{"local-auth":{"settings":{"profile":7}}}}`
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
	user := testconfig.UserFormat(t, configdoc.JSONC, userText)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	var out bytes.Buffer
	rc := configRunW([]string{"check"}, &out, &bytes.Buffer{})
	if rc != 1 || !strings.Contains(out.String(), "settings.profile") || !strings.Contains(out.String(), "expected a string") || !strings.Contains(out.String(), "[SKIP] Selected pack missing declaration checks skipped") {
		t.Fatalf("filtered readable declaration was lost with its unavailable sibling (rc=%d):\n%s", rc, out.String())
	}
}

// A FETCHED pack's filtered declaration keeps its nested loophole module: the store checkout is
// read in place, so the module's typed setting is checked, and no unknown-module warning is raised.
func TestConfigCheckValidatesFilteredFetchedLoopholesFromTheStore(t *testing.T) {
	repo := initFetchedAuthRepo(t)
	source := "git+file://" + repo + "?ref=main"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
	withConfigEditCWD(t, workspace.Root)
	settings := `"loopholes":{"fetched-auth":{"settings":{"profile":7}}}`
	for _, tc := range []struct {
		name, pack string
		filtered   bool
	}{
		{"unfiltered control", `{"source":"` + source + `"}`, false},
		{"filtered", `{"source":"` + source + `","exclude":["README.md"]}`, true},
	} {
		userText := `{"packs":[` + tc.pack + `],` + settings + `}`
		user := testconfig.UserFormat(t, configdoc.JSONC, userText)
		t.Setenv("HOME", user.Root)
		var install bytes.Buffer
		if rc := packMain([]string{"install"}, &install, &install, false); rc != 0 {
			t.Fatalf("%s: pack install rc=%d:\n%s", tc.name, rc, install.String())
		}
		var out bytes.Buffer
		rc := configRunW([]string{"check"}, &out, &bytes.Buffer{})
		if rc != 1 || !strings.Contains(out.String(), "settings.profile") || !strings.Contains(out.String(), "expected a string") {
			t.Errorf("%s: fetched module setting was not validated (rc=%d):\n%s", tc.name, rc, out.String())
		}
		if strings.Contains(out.String(), "no loophole named 'fetched-auth' is installed") {
			t.Errorf("%s: a filtered fetched pack lost its loophole module:\n%s", tc.name, out.String())
		}
	}
}

// initFetchedAuthRepo is a git repository holding one pack with a loophole module that has a typed
// string setting, committed on main.
func initFetchedAuthRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	files := map[string]string{
		"pack.toml":                             "name = \"fetched-auth\"\n[[loophole]]\nfrom = \"loopholes/fetched-auth\"\n",
		"README.md":                             "filtered harmless file\n",
		"loopholes/fetched-auth/manifest.jsonc": `{"name":"fetched-auth","settings":{"profile":{"type":"string","scope":"user","default":"safe"}}}`,
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "pack"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestConfigCheckCanonicalizesQualifiedShippedPackIdentity(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
	user := testconfig.UserFormat(t, configdoc.TOML, "packs = [\"yolo/journal\"]\n[allow.journal]\nalways = [\"not-a-journal-grant\"]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	var out bytes.Buffer
	rc := configRunW([]string{"check"}, &out, &bytes.Buffer{})
	if rc != 1 || !strings.Contains(out.String(), "allow.journal.always lists not-a-journal-grant") {
		t.Fatalf("qualified shipped identity not used by grant validation (rc=%d):\n%s", rc, out.String())
	}
	selection, _, err := config.UserScopeSelectedPackResolution()
	if err != nil || !selection.Complete() {
		t.Fatalf("qualified selected-pack closure = %+v, err %v", selection, err)
	}
	journalCount := 0
	for _, pack := range selection.Packs() {
		if pack.Name == "journal" {
			journalCount++
		}
		if pack.Name == "yolo/journal" {
			t.Fatalf("qualified reference leaked into canonical pack identity: %+v", pack)
		}
	}
	if journalCount != 1 {
		t.Fatalf("canonical journal pack appears %d times in the selected closure", journalCount)
	}
}

func TestConfigCheckDoesNotRunGitForLocalCloneFacts(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
	user := testconfig.UserFormat(t, configdoc.TOML, "local_clones = [\""+workspace.Root+"\"]\n")
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "git-invoked")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf called >> \""+marker+"\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	var out bytes.Buffer
	rc := configRunW([]string{"check"}, &out, &bytes.Buffer{})
	if rc != 0 || !strings.Contains(out.String(), "Merged config is semantically valid") {
		t.Fatalf("local-clone config was not validated without repository probing (rc=%d):\n%s", rc, out.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("config check ran Git outside the fetched-store exception: %v\n%s", err, out.String())
	}
}

func TestConfigCheckSkipsAbsentDefaultRefPinButRejectsMalformedLock(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
	user := testconfig.UserFormat(t, configdoc.TOML, "packs = [\"acme/tools\"]\n[pack_sources.acme]\ngit = \"https://example.invalid/acme/packs.git\"\n")
	lock := filepath.Join(filepath.Dir(user.Path), "packs.lock.json")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	var out bytes.Buffer
	rc := configRunW([]string{"check"}, &out, &bytes.Buffer{})
	if rc != 0 || !strings.Contains(out.String(), "[SKIP] Selected pack acme/tools declaration checks skipped") || !strings.Contains(out.String(), "yolo pack install") {
		t.Fatalf("absent default-ref pin was not reported as partial coverage (rc=%d):\n%s", rc, out.String())
	}
	if err := os.WriteFile(lock, []byte("{malformed"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	rc = configRunW([]string{"check"}, &out, &bytes.Buffer{})
	if rc != 1 || !strings.Contains(out.String(), "could not be validated") || strings.Contains(out.String(), "[SKIP] Selected pack acme/tools") {
		t.Fatalf("malformed lock was not retained as FAIL (rc=%d):\n%s", rc, out.String())
	}
	// A pin written for another source is the state an absent pin is: the same SKIP and remedy.
	stale := `{"schema":1,"packs":{"acme/tools":{"name":"acme/tools","source":"git+https://example.invalid/other/packs.git","commit":"` + strings.Repeat("a", 40) + `","ref":"main"}}}`
	if err := os.WriteFile(lock, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	rc = configRunW([]string{"check"}, &out, &bytes.Buffer{})
	if rc != 0 || !strings.Contains(out.String(), "[SKIP] Selected pack acme/tools declaration checks skipped") || !strings.Contains(out.String(), "yolo pack install") {
		t.Fatalf("stale-source pin was not reported as partial coverage (rc=%d):\n%s", rc, out.String())
	}
}

func TestConfigCheckInJailSuppressesHostMountFindingsAndSerializesScope(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "mounts = [{ host = \"/missing/host/path\", at = \"/ctx/source\", mode = \"ro\" }]\n")
	user := testconfig.UserFormat(t, configdoc.TOML, "")
	t.Setenv("HOME", user.Root)
	withConfigEditCWD(t, workspace.Root)
	// The host control: the same file, read on the host, reports the missing source. Without it the
	// in-jail assertion below could pass for a fixture that is never read.
	t.Setenv("YOLO_VERSION", "")
	var host bytes.Buffer
	if rc := configRunW([]string{"check"}, &host, &bytes.Buffer{}); rc != 0 || !strings.Contains(host.String(), "host path does not exist and will be skipped: /missing/host/path") {
		t.Fatalf("host control did not read the mount fixture (rc=%d):\n%s", rc, host.String())
	}
	t.Setenv("YOLO_VERSION", "in-jail")
	t.Setenv("YOLO_WORKSPACE", workspace.Root)
	var text bytes.Buffer
	rc := configRunW([]string{"check"}, &text, &bytes.Buffer{})
	if rc != 0 || strings.Contains(text.String(), "host path does not exist") || !strings.Contains(text.String(), "Host paths and mounts: this is a generated snapshot; host facts are unverified") {
		t.Fatalf("in-jail host mount facts were reported (rc=%d):\n%s", rc, text.String())
	}
	var jsonOut bytes.Buffer
	if rc := configRunW([]string{"check", "--json"}, &jsonOut, &bytes.Buffer{}); rc != 0 {
		t.Fatalf("in-jail JSON config check rc=%d: %s", rc, jsonOut.String())
	}
	var report struct {
		Scope struct {
			NotChecked []struct{ Area, Reason, Verb string } `json:"not_checked"`
			NotReached []string                              `json:"not_reached"`
		} `json:"scope"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range report.Scope.NotChecked {
		if item.Area == "Host paths and mounts" && strings.Contains(item.Reason, "unverified") && strings.Contains(item.Verb, "on the host") {
			found = true
		}
	}
	if !found {
		t.Fatalf("JSON omitted in-jail host-fact scope: %+v", report.Scope)
	}
}

func TestConfigCheckJSONReportsGatedCoverageAfterParseFailure(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "[machine.resources\n")
	user := testconfig.UserFormat(t, configdoc.TOML, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	var out bytes.Buffer
	rc := configRunW([]string{"check", "--json"}, &out, &bytes.Buffer{})
	if rc != 1 {
		t.Fatalf("malformed config rc=%d: %s", rc, out.String())
	}
	var report struct {
		Scope struct {
			NotReached []string `json:"not_reached"`
		} `json:"scope"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Scope.NotReached) != 2 || !strings.Contains(strings.Join(report.Scope.NotReached, " "), "selected-pack") {
		t.Fatalf("JSON did not disclose gated config coverage: %+v", report.Scope)
	}
	var text bytes.Buffer
	if rc := configRunW([]string{"check"}, &text, &bytes.Buffer{}); rc != 1 || !strings.Contains(text.String(), "Not reached: selected-pack declarations and cross-pack checks") {
		t.Fatalf("text did not disclose gated config coverage (rc=%d):\n%s", rc, text.String())
	}
}
