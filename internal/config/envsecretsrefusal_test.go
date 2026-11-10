package config

import (
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func userSite(string, bool) EnvSite { return EnvSite{Scope: EnvScopeUser} }

// secret-env-scoping.md §3.5, the row 2 refusal, exactly; its line breaks are the design's wrapping.
func TestSecretEnvRefusalNamesAPublicProviderKeyAndTheMove(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	h := NewHydratedEnv()
	h.SetPublic("ZAI_API_KEY", "zai-probe", EnvPlace{Key: "env.files", Entry: "~/.config/yolo-jail/defaults.env"})
	got := SecretEnvRefusals(h, []packload.PublicClaim{{Name: "ZAI_API_KEY", Providers: []string{"zai"}}}, userSite)
	want := "config.env.files: ZAI_API_KEY is set in your public environment ([env], from " +
		"~/.config/yolo-jail/defaults.env), and the zai provider this launch selects claims it as its key. A " +
		"public value reaches every process in the jail, so a provider's key may not live there. Move it to the " +
		"private set: yolo config secret ZAI_API_KEY --user"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("want\n%q\ngot\n%q", want, got)
	}
}

// §4.8, the both-sets refusal, exactly: one paragraph, its two commands each on their own line.
func TestSecretEnvRefusalNamesANameInBothSetsAndBothDrops(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	h := NewHydratedEnv()
	h.SetPublic("ZAI_API_KEY", "a", EnvPlace{Key: "env.files", Entry: "~/.config/yolo-jail/defaults.env"})
	h.SetPrivate("ZAI_API_KEY", "b", EnvPlace{Private: true, Key: "env.secrets.files", Entry: "~/.config/yolo-jail/secrets.env"})
	got := SecretEnvRefusals(h, []packload.PublicClaim{{Name: "ZAI_API_KEY", Providers: []string{"zai"}}}, userSite)
	want := "config.env: ZAI_API_KEY is set in both your public environment ([env], from " +
		"~/.config/yolo-jail/defaults.env) and your private one ([env.secrets], from " +
		"~/.config/yolo-jail/secrets.env). A name lives in one set. Keep the private copy:\n" +
		"yolo config secret ZAI_API_KEY --drop --user\n" +
		"or keep the public copy: yolo config unsecret ZAI_API_KEY --drop --user"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("one both-sets refusal and no row 2 line for the same name; want\n%q\ngot\n%q", want, got)
	}
}

// SE-D6: a legacy file has no private set, so its remedy names `yolo config migrate` first. The
// load lowers a legacy `env_sources` to the place key env.sources, so the refusal names the key the
// user's file spells (SEP-D7: `(env_sources, from X)`), from the site's Legacy bit.
func TestSecretEnvRefusalOnALegacyFileNamesMigrateFirst(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	legacy := func(string, bool) EnvSite { return EnvSite{Scope: EnvScopeUser, Legacy: true} }
	claims := []packload.PublicClaim{{Name: "ZAI_API_KEY", Providers: []string{"zai"}}}
	for _, tc := range []struct {
		entry, place string
	}{
		{"", "(env_sources)"},
		{"/home/u/keys.env", "(env_sources, from /home/u/keys.env)"},
	} {
		h := NewHydratedEnv()
		h.SetPublic("ZAI_API_KEY", "z", EnvPlace{Key: "env.sources", Entry: tc.entry})
		got := SecretEnvRefusals(h, claims, legacy)
		if len(got) != 1 || !strings.HasPrefix(got[0], "config.env_sources: ZAI_API_KEY is set in your public environment "+tc.place+",") ||
			!strings.Contains(got[0], "run yolo config migrate, then yolo config secret ZAI_API_KEY --user") {
			t.Errorf("entry %q: got %q", tc.entry, got)
		}
	}
}

// SEP-D17, SEP-D8: one dotenv file in both lists is one refusal naming the file move, ahead of
// the per-name both-sets lines and the row 2 lines; and no value appears anywhere.
func TestSecretEnvRefusalNamesASharedFileOnceAndFirst(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	h := NewHydratedEnv()
	shared := "/home/u/.config/yolo-jail/keys.env"
	for _, n := range []string{"A_KEY", "B_KEY"} {
		h.SetPublic(n, "v-"+n, EnvPlace{Key: "env.files", Entry: shared})
		h.SetPrivate(n, "v-"+n, EnvPlace{Private: true, Key: "env.secrets.files", Entry: shared})
	}
	h.SetPublic("FOO", "f", EnvPlace{Key: "env.set"})
	h.SetPrivate("FOO", "g", EnvPlace{Private: true, Key: "env.secrets.set"})
	h.SetPublic("ZAI_API_KEY", "zai-probe", EnvPlace{Key: "env.set"})
	got := SecretEnvRefusals(h, []packload.PublicClaim{{Name: "ZAI_API_KEY", Providers: []string{"zai"}}}, userSite)
	if len(got) != 3 {
		t.Fatalf("a shared-file line, a both-sets line, a row 2 line; got %q", got)
	}
	if !strings.Contains(got[0], shared) || !strings.Contains(got[0], "yolo config secret --dotenv "+shared+" --user") ||
		!strings.Contains(got[0], "yolo config unsecret --dotenv "+shared+" --user") {
		t.Errorf("the shared file comes first and names both file moves: %q", got[0])
	}
	if !strings.HasPrefix(got[1], "config.env: FOO is set in both") || !strings.HasPrefix(got[2], "config.env.set: ZAI_API_KEY") {
		t.Errorf("then both-sets, then row 2: %q", got)
	}
	for _, line := range got {
		if strings.Contains(line, "v-") || strings.Contains(line, "zai-probe") {
			t.Errorf("a refusal never carries a value: %q", line)
		}
	}
}

// Nothing to refuse: a private claimed key, and a public name nothing claims.
func TestSecretEnvRefusalIsEmptyForAPrivateKeyAndAnUnclaimedPublicName(t *testing.T) {
	h := NewHydratedEnv()
	h.SetPrivate("ZAI_API_KEY", "z", EnvPlace{Private: true, Key: "env.secrets.set"})
	h.SetPublic("DEBUG", "1", EnvPlace{Key: "env.set"})
	if got := SecretEnvRefusals(h, nil, userSite); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

// A shared file's remedy carries the flag of the scope that lists the file, never the flag of a
// name's own site in another scope (review minor 8).
func TestSharedFileRefusalNeverBorrowsAnotherScopesFlag(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	h := NewHydratedEnv()
	shared := "/home/u/.config/yolo-jail/keys.env"
	h.SetPublic("A_KEY", "v", EnvPlace{Key: "env.files", Entry: shared})
	h.SetPrivate("A_KEY", "v", EnvPlace{Private: true, Key: "env.secrets.files", Entry: shared})
	// The name's first public site is an inline value in the project config.
	otherScope := func(string, bool) EnvSite {
		return EnvSite{Scope: EnvScopeProject, Place: EnvPlace{Key: "env.set"}}
	}
	got := SecretEnvRefusals(h, nil, otherScope)
	if len(got) != 1 || strings.Contains(got[0], "--project") {
		t.Errorf("the file is listed by another scope than --project: %q", got)
	}
}

// One file spelled two ways is one shared file: the launch says so, steering the user to the
// file move and never into a --drop (re-review blocker).
func TestSecretEnvRefusalFindsASharedFileByIdentity(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	dir := t.TempDir()
	real := dir + "/keys.env"
	if err := os.WriteFile(real, []byte("A_KEY=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, dir+"/alias.env"); err != nil {
		t.Fatal(err)
	}
	h := NewHydratedEnv()
	h.SetPublic("A_KEY", "v", EnvPlace{Key: "env.files", Entry: real})
	h.SetPrivate("A_KEY", "v", EnvPlace{Private: true, Key: "env.secrets.files", Entry: dir + "/alias.env"})
	got := SecretEnvRefusals(h, nil, userSite)
	if len(got) != 1 || !strings.Contains(got[0], "yolo config secret --dotenv") || strings.Contains(got[0], "--drop") {
		t.Errorf("want the shared-file refusal, got %q", got)
	}
}
