package config

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestAllowPackNudgesJudgesTablesAgainstThePackList(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	allow := jsonx.NewOrderedMap()
	for _, name := range []string{"aws-auth", "aws-aut", "claude", "off-one", "mounts"} {
		table := jsonx.NewOrderedMap()
		table.Set("x", "y")
		allow.Set(name, table)
	}
	allow.Set("off-one", "off")
	user := jsonx.NewOrderedMap()
	user.Set("allow", allow)
	facts := AllowPackFacts{
		Listed:       []string{"claude"},
		Names:        map[string]string{"aws-auth": "aws-auth", "claude": "claude", "off-one": "off-one"},
		LoginProfile: map[string]string{"claude": "profile"},
	}

	got := AllowPackNudges(user, nil, facts)

	kinds := map[string]AllowNudgeKind{}
	for _, n := range got {
		kinds[n.Table] = n.Kind
	}
	want := map[string]AllowNudgeKind{
		"aws-auth": AllowNudgeUnselected,
		"aws-aut":  AllowNudgeUnknown,
		"claude":   AllowNudgeLoginNoProfile,
	}
	if len(kinds) != len(want) {
		t.Fatalf("findings = %v, want exactly %v (mounts and an `off` tombstone are not findings)", kinds, want)
	}
	for table, kind := range want {
		if kinds[table] != kind {
			t.Errorf("allow.%s = %q, want %q", table, kinds[table], kind)
		}
	}
	for _, n := range got {
		if n.Kind == AllowNudgeUnknown && n.Closest != "aws-auth" {
			t.Errorf("closest for aws-aut = %q, want aws-auth", n.Closest)
		}
	}
}

// In a jail the user scope is the inherited copy, which carries `packs` but never `allow`
// (inherit.go: allow is the host's authority and crosses into no jail file). A listed login pack
// therefore always looks profile-less there, whatever the host file says, so the
// login-no-profile finding cannot be judged and must not fire. The fixture is the shape a host
// writes: packs in inherited-launch.toml, no allow anywhere.
func TestAllowPackNudgesSkipsLoginNoProfileInAJail(t *testing.T) {
	home := layerHome(t, "")
	t.Setenv(UserLayerEnv, "")
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	write(t, filepath.Join(home, ".config", "yolo-jail", "inherited-launch.toml"), "packs = [\"aws-auth\"]\n")

	user, src, err := UserScopeConfigWithSources(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := ListedPackNames(nil)
	if err != nil {
		t.Fatal(err)
	}
	facts := EmbeddedAllowPackFacts(listed)
	if facts.LoginProfile["aws-auth"] == "" || !readableSet(listed)["aws-auth"] {
		t.Fatalf("fixture does not exercise the finding: listed=%v login=%v", listed, facts.LoginProfile)
	}
	for _, n := range AllowPackNudges(user, src, facts) {
		t.Errorf("in a jail, which is never handed [allow], found %q: %s", n.Kind, n.Message())
	}
}

// On the host the same list with no [allow.aws-auth] profile is a real finding.
func TestAllowPackNudgesWarnsLoginNoProfileOnTheHost(t *testing.T) {
	home := layerHome(t, "")
	t.Setenv(UserLayerEnv, "")
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), "packs = [\"aws-auth\"]\n")

	user, src, err := UserScopeConfigWithSources(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := ListedPackNames(nil)
	if err != nil {
		t.Fatal(err)
	}
	got := AllowPackNudges(user, src, EmbeddedAllowPackFacts(listed))
	if len(got) != 1 || got[0].Kind != AllowNudgeLoginNoProfile || got[0].Pack != "aws-auth" {
		t.Fatalf("host findings = %+v, want one login-no-profile for aws-auth", got)
	}
}
