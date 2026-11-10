package cli

// configsecret_test.go drives `yolo config secret|unsecret` through configRunW, the real dispatch
// (docs/design/secret-env-scoping.md SE-D9, §3.5; secret-env-scoping-plan.md Task 5).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// secretFixture is a TOML user config at a fresh HOME, outside any jail.
func secretFixture(t *testing.T, body string) testconfig.Fixture {
	t.Helper()
	user := testconfig.User(t, body)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	return user
}

func TestConfigSecretMovesOneDotenvLineToThePrivateFile(t *testing.T) {
	user := secretFixture(t, "[env]\nfiles = [\"defaults.env\"]\n\n[env.secrets]\nfiles = [\"secrets.env\"]\n")
	dir := filepath.Dir(user.Path)
	pub, priv := filepath.Join(dir, "defaults.env"), filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(pub, []byte("GH_TOKEN=g\nexport ZAI_API_KEY=\"zai-value\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	rc := configRunW([]string{"secret", "ZAI_API_KEY", "--user"}, &out, &errw)
	if rc != 0 {
		t.Fatalf("rc %d: %s", rc, errw.String())
	}
	if got, _ := os.ReadFile(pub); string(got) != "GH_TOKEN=g\n" {
		t.Errorf("the public file keeps only the other line: %q", got)
	}
	if got, _ := os.ReadFile(priv); string(got) != "export ZAI_API_KEY=\"zai-value\"\n" {
		t.Errorf("the private file gains the line verbatim: %q", got)
	}
	if info, err := os.Stat(priv); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("a created private file is 0600, got %v (%v)", info, err)
	}
	if strings.Contains(out.String()+errw.String(), "zai-value") {
		t.Error("the verb never prints a value")
	}
	if !strings.Contains(out.String(), "moved ZAI_API_KEY from ([env], from "+pub+") to ([env.secrets], from "+priv+")") {
		t.Errorf("one line names both places:\n%s", out.String())
	}
}

func TestConfigSecretMovesAnInlineValue(t *testing.T) {
	user := secretFixture(t, "[env]\nset = { ZAI_API_KEY = \"zai-value\", DEBUG = \"1\" }\n")
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"secret", "ZAI_API_KEY", "--user"}, &out, &errw); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errw.String())
	}
	got, _ := os.ReadFile(user.Path)
	doc, err := configdoc.Parse(got, configdoc.TOML)
	if err != nil {
		t.Fatalf("the edited config parses: %v\n%s", err, got)
	}
	_, private, _ := doc.Index.Value(configdoc.Step{Key: "env"}, configdoc.Step{Key: "secrets"}, configdoc.Step{Key: "set"}, configdoc.Step{Key: "ZAI_API_KEY"})
	_, public, _ := doc.Index.Value(configdoc.Step{Key: "env"}, configdoc.Step{Key: "set"}, configdoc.Step{Key: "ZAI_API_KEY"})
	_, debug, _ := doc.Index.Value(configdoc.Step{Key: "env"}, configdoc.Step{Key: "set"}, configdoc.Step{Key: "DEBUG"})
	if !private || public || !debug || strings.Count(string(got), "ZAI_API_KEY") != 1 {
		t.Errorf("the inline value moved to [env.secrets] set and DEBUG stayed:\n%s", got)
	}
	if strings.Contains(out.String()+errw.String(), "zai-value") {
		t.Errorf("the verb never prints a value:\n%s%s", out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "moved ZAI_API_KEY from ([env] set) to ([env.secrets] set)") {
		t.Errorf("one line names both places:\n%s", out.String())
	}
}

func TestConfigSecretWithNoPrivateFileNamesTheOneToAddFirst(t *testing.T) {
	user := secretFixture(t, "[env]\nfiles = [\"defaults.env\"]\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(user.Path), "defaults.env"), []byte("ZAI_API_KEY=z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	rc := configRunW([]string{"secret", "ZAI_API_KEY", "--user"}, &out, &errw)
	want := `no private file to move ZAI_API_KEY into; run yolo config add env.secrets.files '"~/.config/yolo-jail/secrets.env"' --user, then this command again`
	if rc == 0 || !strings.Contains(errw.String(), want) {
		t.Errorf("rc %d, stderr %q", rc, errw.String())
	}
}

// SEP-D13: a name in an inline env.sources table moves into the other set's `set` table.
func TestConfigSecretMovesANameOutOfAnInlineSourcesTable(t *testing.T) {
	user := secretFixture(t, "[env]\nsources = [{ set = { ZAI_API_KEY = \"zai-value\", DEBUG = \"1\" } }]\n")
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"secret", "ZAI_API_KEY", "--user"}, &out, &errw); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errw.String())
	}
	got, _ := os.ReadFile(user.Path)
	doc, err := configdoc.Parse(got, configdoc.TOML)
	if err != nil {
		t.Fatalf("the edited config parses: %v\n%s", err, got)
	}
	_, private, _ := doc.Index.Value(configdoc.Step{Key: "env"}, configdoc.Step{Key: "secrets"}, configdoc.Step{Key: "set"}, configdoc.Step{Key: "ZAI_API_KEY"})
	if !private || strings.Count(string(got), "ZAI_API_KEY") != 1 || !strings.Contains(string(got), "DEBUG") {
		t.Errorf("the name left the sources table for [env.secrets] set, DEBUG stayed:\n%s", got)
	}
	if !strings.Contains(out.String(), "moved ZAI_API_KEY from ([env] sources) to ([env.secrets] set)") ||
		strings.Contains(out.String()+errw.String(), "zai-value") {
		t.Errorf("one line names both places and no value:\n%s%s", out.String(), errw.String())
	}
}

// SEP-D14: the unsecret mirror suggests defaults.env.
func TestConfigUnsecretWithNoPublicFileNamesDefaultsEnv(t *testing.T) {
	user := secretFixture(t, "[env.secrets]\nfiles = [\"secrets.env\"]\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(user.Path), "secrets.env"), []byte("STRIPE_KEY=s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	rc := configRunW([]string{"unsecret", "STRIPE_KEY"}, &out, &errw)
	want := `no public file to move STRIPE_KEY into; run yolo config add env.files '"~/.config/yolo-jail/defaults.env"' --user, then this command again`
	if rc == 0 || !strings.Contains(errw.String(), want) {
		t.Errorf("rc %d, stderr %q", rc, errw.String())
	}
}

func TestConfigSecretDropKeepsThePrivateCopy(t *testing.T) {
	user := secretFixture(t, "[env]\nset = { ZAI_API_KEY = \"old\" }\n\n[env.secrets]\nset = { ZAI_API_KEY = \"new\" }\n")
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"secret", "ZAI_API_KEY", "--drop", "--user"}, &out, &errw); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errw.String())
	}
	got, _ := os.ReadFile(user.Path)
	if strings.Contains(string(got), "old") || !strings.Contains(string(got), "new") {
		t.Errorf("--drop removes the public copy and keeps the private one:\n%s", got)
	}
}

func TestConfigUnsecretMovesBack(t *testing.T) {
	user := secretFixture(t, "[env]\nfiles = [\"defaults.env\"]\n\n[env.secrets]\nfiles = [\"secrets.env\"]\n")
	dir := filepath.Dir(user.Path)
	pub, priv := filepath.Join(dir, "defaults.env"), filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(priv, []byte("STRIPE_KEY='s-value'\nZAI_API_KEY=z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"unsecret", "STRIPE_KEY"}, &out, &errw); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errw.String())
	}
	if got, _ := os.ReadFile(priv); string(got) != "ZAI_API_KEY=z\n" {
		t.Errorf("the private file keeps only the other line: %q", got)
	}
	if got, _ := os.ReadFile(pub); string(got) != "STRIPE_KEY='s-value'\n" {
		t.Errorf("the public file gains the line verbatim: %q", got)
	}
	if strings.Contains(out.String()+errw.String(), "s-value") {
		t.Error("the verb never prints a value")
	}
}

// SEP-D17: one dotenv file listed in both sets moves as a file entry, not name by name.
func TestConfigSecretDotenvMovesTheFileEntry(t *testing.T) {
	user := secretFixture(t, "[env]\nfiles = [\"keys.env\", \"other.env\"]\n\n[env.secrets]\nfiles = [\"keys.env\"]\n")
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"secret", "--dotenv", "keys.env", "--user"}, &out, &errw); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errw.String())
	}
	got, _ := os.ReadFile(user.Path)
	cfg := string(got)
	if strings.Count(cfg, "keys.env") != 1 || !strings.Contains(cfg, "other.env") {
		t.Errorf("keys.env leaves [env] files and stays in [env.secrets] files once:\n%s", cfg)
	}
	before, after, _ := strings.Cut(cfg, "[env.secrets]")
	if strings.Contains(before, "keys.env") || !strings.Contains(after, "keys.env") {
		t.Errorf("keys.env is listed by the private set alone:\n%s", cfg)
	}
}

func TestConfigSecretOnALegacyConfigNamesMigrateFirst(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{"env_sources": [{"ZAI_API_KEY": "z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	var out, errw bytes.Buffer
	rc := configRunW([]string{"secret", "ZAI_API_KEY"}, &out, &errw)
	if rc == 0 || !strings.Contains(errw.String(), "run yolo config migrate, then yolo config secret ZAI_API_KEY --user") {
		t.Errorf("rc %d, stderr %q", rc, errw.String())
	}
}

func TestConfigSecretOfANameSetNowhereSaysSo(t *testing.T) {
	secretFixture(t, "[env]\nset = { DEBUG = \"1\" }\n")
	var out, errw bytes.Buffer
	rc := configRunW([]string{"secret", "ZAI_API_KEY"}, &out, &errw)
	if rc == 0 || !strings.Contains(errw.String(), "ZAI_API_KEY is set in no config's [env]") {
		t.Errorf("rc %d, stderr %q", rc, errw.String())
	}
}

// A file both sets list is one copy of its keys: suggesting it as the destination, or dropping
// from it, would delete the only copy (review blocker 1).
func TestConfigSecretNeverSuggestsAFileTheOtherSetLists(t *testing.T) {
	user := secretFixture(t, "[env]\nfiles = [\"defaults.env\", \"secrets.env\"]\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(user.Path), "defaults.env"), []byte("ZAI_API_KEY=z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	rc := configRunW([]string{"secret", "ZAI_API_KEY", "--user"}, &out, &errw)
	if rc == 0 || strings.Contains(errw.String(), "/secrets.env") || !strings.Contains(errw.String(), "private.env") {
		t.Errorf("rc %d: the suggestion must not be a file [env] files lists:\n%s", rc, errw.String())
	}
}

func TestConfigSecretDropRefusesWhenBothSetsListTheSameFile(t *testing.T) {
	user := secretFixture(t, "[env]\nfiles = [\"keys.env\"]\n\n[env.secrets]\nfiles = [\"keys.env\"]\n")
	keys := filepath.Join(filepath.Dir(user.Path), "keys.env")
	if err := os.WriteFile(keys, []byte("ZAI_API_KEY=z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"secret", "unsecret"} {
		var out, errw bytes.Buffer
		rc := configRunW([]string{verb, "ZAI_API_KEY", "--drop", "--user"}, &out, &errw)
		if got, _ := os.ReadFile(keys); rc == 0 || string(got) != "ZAI_API_KEY=z\n" {
			t.Errorf("%s --drop must leave the only copy: rc %d, file %q", verb, rc, got)
		}
		if !strings.Contains(errw.String(), "--dotenv") {
			t.Errorf("%s: the refusal points at --dotenv:\n%s", verb, errw.String())
		}
	}
}

// Review major 4: a name private in the user config and public in the project's. The refusal's
// remedy must work, and must never move the repo's value into the user's private set.
func TestConfigSecretDropWorksAcrossScopes(t *testing.T) {
	user := secretFixture(t, "[env.secrets]\nset = { FOO = \"user-value\" }\n")
	ws, _ := os.Getwd()
	project := filepath.Join(ws, "yolo-jail.toml")
	if err := os.WriteFile(project, []byte("[env]\nset = { FOO = \"repo-value\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"secret", "FOO", "--project"}, &out, &errw); rc == 0 {
		t.Errorf("a plain move would put the repo's value beside the user's private one")
	}
	if got, _ := os.ReadFile(project); !strings.Contains(string(got), "repo-value") {
		t.Errorf("the refused move left the project config alone:\n%s", got)
	}
	out.Reset()
	errw.Reset()
	if rc := configRunW([]string{"secret", "FOO", "--drop", "--project"}, &out, &errw); rc != 0 {
		t.Fatalf("the remedy fails across scopes, rc %d: %s", rc, errw.String())
	}
	if got, _ := os.ReadFile(project); strings.Contains(string(got), "FOO") {
		t.Errorf("--drop removed the project's public copy:\n%s", got)
	}
	if got, _ := os.ReadFile(user.Path); !strings.Contains(string(got), "user-value") || strings.Contains(string(got), "repo-value") {
		t.Errorf("the user's private value is kept:\n%s", got)
	}
}

// Review minor 6: a dotenv file that is a link is neither written through nor replaced.
func TestConfigSecretRefusesADotenvFileThatIsALink(t *testing.T) {
	user := secretFixture(t, "[env]\nfiles = [\"defaults.env\"]\n\n[env.secrets]\nfiles = [\"secrets.env\"]\n")
	dir := filepath.Dir(user.Path)
	real := filepath.Join(t.TempDir(), "real.env")
	if err := os.WriteFile(real, []byte("ZAI_API_KEY=z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "defaults.env")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	rc := configRunW([]string{"secret", "ZAI_API_KEY", "--user"}, &out, &errw)
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v %v", info, err)
	}
	if got, _ := os.ReadFile(real); rc == 0 || string(got) != "ZAI_API_KEY=z\n" || !strings.Contains(errw.String(), "symbolic link") {
		t.Errorf("rc %d, target %q, stderr %q", rc, got, errw.String())
	}
}
