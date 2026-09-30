package packdecl

// regionfile_test.go pins a provider's `region_file` and an env contribution's
// `region_profile_setting` on the manifest decoder a launch reads packs through
// (docs/design/bedrock-plumbing.md BR-DIR1, BR-D21): the pack facts that say where a platform's
// agents keep a region, and which loophole setting names the profile a served credential is for.

import (
	"strings"
	"testing"
)

// regionFileProvider is a provider declaring an invented platform's region file.
const regionFileProvider = `{"kind":"provider","name":"regional","platform":"cloud",
  "region_env_name":["CLOUD_REGION"],
  "region_file":{"path":".cloud/config","path_env_name":"CLOUD_CONFIG","profile_env_name":"CLOUD_PROFILE",
    "default_profile":"default","profile_section":"profile {profile}","key":"region"}}`

// The declaration decodes whole onto the provider projection packload's region fill walks, and
// names each profile's sections: every profile through the template, and the default profile by
// its bare name too, after the template's spelling, which takes priority.
func TestAProviderMayDeclareItsRegionFile(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[` + regionFileProvider + `]}`))
	if len(problems) != 0 {
		t.Fatalf("a region_file is legal: %v", problems)
	}
	f := m.Providers()[0].RegionFile
	if f == nil {
		t.Fatal("region_file must reach the provider projection")
	}
	if f.Path != ".cloud/config" || f.PathEnvName != "CLOUD_CONFIG" || f.ProfileEnvName != "CLOUD_PROFILE" ||
		f.Key != "region" {
		t.Errorf("region_file decoded as %+v", *f)
	}
	if got := f.Section("dev"); got != "profile dev" {
		t.Errorf("profile dev's section = %q, want \"profile dev\"", got)
	}
	if got := strings.Join(f.Sections("dev"), "|"); got != "profile dev" {
		t.Errorf("profile dev's sections = %q, want \"profile dev\" alone", got)
	}
	if got := strings.Join(f.Sections("default"), "|"); got != "profile default|default" {
		t.Errorf("the default profile's sections = %q, want the template's spelling, then its bare name", got)
	}
}

// Each malformed spelling is refused, naming the field it is in.
func TestARegionFileIsValidated(t *testing.T) {
	good := map[string]string{"path": `".cloud/config"`, "default_profile": `"default"`,
		"profile_section": `"profile {profile}"`, "key": `"region"`}
	body := func(field, value string) string {
		var parts []string
		for k, v := range good {
			if k == field {
				v = value
			}
			parts = append(parts, `"`+k+`":`+v)
		}
		if _, known := good[field]; !known {
			parts = append(parts, `"`+field+`":`+value)
		}
		return `{"contributes":[{"kind":"provider","name":"p","platform":"cloud","region_env_name":["CLOUD_REGION"],` +
			`"region_file":{` + strings.Join(parts, ",") + `}}]}`
	}
	for _, tc := range []struct{ name, field, value, want string }{
		{"no path", "path", `""`, "region_file.path"},
		{"an absolute path", "path", `"/etc/cloud"`, "region_file.path"},
		{"a tilde path", "path", `"~/.cloud/config"`, "region_file.path"},
		{"a path climbing out of the home", "path", `"../elsewhere"`, "region_file.path"},
		{"an unclean path", "path", `".cloud//config"`, "region_file.path"},
		{"a bad path variable", "path_env_name", `"NOT-A-NAME"`, "region_file.path_env_name"},
		{"a bad profile variable", "profile_env_name", `"1BAD"`, "region_file.profile_env_name"},
		{"no default profile", "default_profile", `""`, "region_file.default_profile"},
		{"a section with no placeholder", "profile_section", `"profile"`, "region_file.profile_section"},
		{"a section with brackets", "profile_section", `"[profile {profile}]"`, "region_file.profile_section"},
		{"no key", "key", `""`, "region_file.key"},
		{"a key with an equals sign", "key", `"re=gion"`, "region_file.key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Decode([]byte(body(tc.field, tc.value)))
			if got := strings.Join(problems, "\n"); !strings.Contains(got, tc.want) {
				t.Errorf("%s must be refused naming %s:\n%s", tc.name, tc.want, got)
			}
		})
	}

	// It needs region_env_name, the variables its region is delivered in; and it is a provider's.
	_, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"p","platform":"cloud",` +
		`"region_file":{"path":".c","default_profile":"default","profile_section":"profile {profile}","key":"region"}}]}`))
	if got := strings.Join(problems, "\n"); !strings.Contains(got, `needs "region_env_name" beside it`) {
		t.Errorf("a region_file with no region_env_name must be refused:\n%s", got)
	}
	_, problems = Decode([]byte(`{"contributes":[{"kind":"env","vars":{"A":"b"},` +
		`"region_file":{"path":".c","default_profile":"default","profile_section":"profile {profile}","key":"region"}}]}`))
	if got := strings.Join(problems, "\n"); !strings.Contains(got, `does not take "region_file"`) {
		t.Errorf("a region_file on an env contribution must be refused:\n%s", got)
	}
}

// `region_profile_setting` is an env contribution's, and needs `served_by` (it names that
// loophole's setting) and a `platform` gate (the profile picks a section of that platform's
// region file). The shape aws-auth's pointer ships is legal and reaches the gated projection.
func TestARegionProfileSettingNeedsItsLoopholeAndPlatform(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[{"kind":"env","platform":"cloud","served_by":"cloud-auth",` +
		`"region_profile_setting":"profile","vars":{"CLOUD_URI":"http://{listen}/x"}}]}`))
	if len(problems) != 0 {
		t.Fatalf("aws-auth's shape is legal: %v", problems)
	}
	if got := m.GatedEnvContributions()[0].RegionProfileSetting; got != "profile" {
		t.Errorf("region_profile_setting must reach the gated env projection, got %q", got)
	}
	for _, tc := range []struct{ name, body, want string }{
		{"no served_by", `{"kind":"env","platform":"cloud","region_profile_setting":"profile","vars":{"A":"b"}}`,
			`needs "served_by" beside it`},
		{"no platform gate", `{"kind":"env","served_by":"cloud-auth","region_profile_setting":"profile","vars":{"A":"b"}}`,
			`needs a "platform" gate`},
		{"a bad name", `{"kind":"env","platform":"cloud","served_by":"cloud-auth","region_profile_setting":"a b","vars":{"A":"b"}}`,
			"is not a setting name"},
		{"another kind", `{"kind":"provider","name":"p","region_profile_setting":"profile"}`,
			`does not take "region_profile_setting"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Decode([]byte(`{"contributes":[` + tc.body + `]}`))
			if got := strings.Join(problems, "\n"); !strings.Contains(got, tc.want) {
				t.Errorf("%s must be refused saying %q:\n%s", tc.name, tc.want, got)
			}
		})
	}
}
