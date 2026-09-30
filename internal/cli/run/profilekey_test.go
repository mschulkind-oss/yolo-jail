package run

// profilekey_test.go pins the LAUNCH's call site for the config `profile` key
// (docs/design/providers-and-profiles-redesign.md PP-D10): the key reaches the jail's table
// (YOLO_USE_PROFILES) through effectiveUseProfiles, each form delivering exactly what its -p
// twin delivers, and every -p beating the key. Asserted on the ASSEMBLED env, the launch's
// contract with the jail, so a merge that stopped reading the key — or read it some other way
// than the flag — fails here whatever the fold's own tests say.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// profileKeyConfig is a merged config carrying `"profile": <value>` and nothing else.
func profileKeyConfig(t *testing.T, value string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return newConfig("profile", v)
}

// deliveredProfiles is the one YOLO_USE_PROFILES line a launch assembled with cfg and set
// hands the jail.
func deliveredProfiles(t *testing.T, cfg *jsonx.OrderedMap, set func(*Options)) string {
	t.Helper()
	la := assembleWithProfilesAssembled(t, cfg, packsFixture(t, "claude", "bedrock", "pi", "openai-auth"), set)
	got := la.channelEnv(t, "YOLO_USE_PROFILES")
	if len(got) != 1 {
		t.Fatalf("YOLO_USE_PROFILES crossed %q, want exactly one line", got)
	}
	return got[0]
}

// Each key form delivers the bytes its -p twin delivers. Deleting the key's read from
// effectiveUseProfiles leaves the key column empty; reading "*" as a CLI name puts `"*"` in
// the jail's table; either fails a row.
func TestAssembleProfileKeyDeliversWhatItsFlagDelivers(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		flag func(*Options)
		want string
	}{
		{"a name for every agent", `"bedrock"`,
			func(o *Options) { o.ProfileName = "bedrock" },
			`YOLO_USE_PROFILES={"claude": "bedrock", "pi": "bedrock"}`},
		{"per agent", `{"pi": "codex", "claude": "bedrock"}`,
			func(o *Options) { o.UseProfiles = map[string]string{"pi": "codex", "claude": "bedrock"} },
			`YOLO_USE_PROFILES={"claude": "bedrock", "pi": "codex"}`},
		{"\"*\" for every agent not named", `{"*": "bedrock", "pi": "codex"}`,
			func(o *Options) { o.ProfileName = "bedrock"; o.UseProfiles = map[string]string{"pi": "codex"} },
			`YOLO_USE_PROFILES={"claude": "bedrock", "pi": "codex"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fromKey := deliveredProfiles(t, profileKeyConfig(t, tc.key), nil)
			fromFlag := deliveredProfiles(t, newConfig(), tc.flag)
			if fromKey != tc.want || fromFlag != tc.want {
				t.Errorf("the key delivered %s and -p delivered %s, want both %s", fromKey, fromFlag, tc.want)
			}
		})
	}
}

// PRECEDENCE, kept from before the rename: every -p beats the persistent key for each CLI it
// reaches. A bare -p replaces the key's named entries too, and a pair replaces its CLI's entry
// whatever form the key used.
func TestAssembleEveryFlagBeatsTheProfileKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		flag func(*Options)
		want string
	}{
		{"a bare -p over a named entry", `{"pi": "codex"}`,
			func(o *Options) { o.ProfileName = "bedrock" },
			`YOLO_USE_PROFILES={"pi": "bedrock", "claude": "bedrock"}`},
		{"a pair over \"*\"", `{"*": "bedrock"}`,
			func(o *Options) { o.UseProfiles = map[string]string{"pi": "codex"} },
			`YOLO_USE_PROFILES={"claude": "bedrock", "pi": "codex"}`},
		{"a pair over the string form", `"bedrock"`,
			func(o *Options) { o.UseProfiles = map[string]string{"pi": "codex"} },
			`YOLO_USE_PROFILES={"claude": "bedrock", "pi": "codex"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deliveredProfiles(t, profileKeyConfig(t, tc.key), tc.flag); got != tc.want {
				t.Errorf("delivered %s, want %s", got, tc.want)
			}
		})
	}
}

// WITHIN -p, A PAIR KEEPS ITS CLI BESIDE A BARE NAME (PP-D10, the equation `-p bedrock -p
// pi=codex` = `{"*": "bedrock", "pi": "codex"}`). Before the ruling the bare name was folded
// last and overwrote the pair, so `-p bedrock -p pi=codex` ran pi on bedrock and the pair did
// nothing.
func TestAssembleAPairKeepsItsCLIBesideABareProfile(t *testing.T) {
	got := deliveredProfiles(t, newConfig(), func(o *Options) {
		o.ProfileName = "bedrock"
		o.UseProfiles = map[string]string{"pi": "codex"}
	})
	if want := `YOLO_USE_PROFILES={"claude": "bedrock", "pi": "codex"}`; got != want {
		t.Errorf("delivered %s, want %s", got, want)
	}
}

// The RETIRED key selects nothing at launch: validation refuses it before a launch gets here
// on the host, and in-jail (where it only warns) no reader consults it, so an old snapshot
// cannot select behind the new key's back.
func TestAssembleIgnoresTheRetiredUseProfilesKey(t *testing.T) {
	v, _ := jsonx.Decode([]byte(`{"claude": "bedrock"}`))
	la := assembleWithProfilesAssembled(t, newConfig("use_profiles", v), packsFixture(t, "claude", "bedrock"), nil)
	if got := la.channelEnv(t, "YOLO_USE_PROFILES"); len(got) != 1 || got[0] != "YOLO_USE_PROFILES={}" {
		t.Errorf("the retired key selected %q, want the empty table", got)
	}
}
