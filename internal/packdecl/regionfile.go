package packdecl

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RegionFile is a provider's `region_file` (Contribution.RegionFile): where an agent on this
// provider's PLATFORM finds its region when the composed entry sets none and no region variable
// reaches it — one key in one section of a shared configuration file, the section chosen by a
// profile name. packs/bedrock declares AWS's: the `region` key of `[profile NAME]`, or of
// `[default]` for the profile named "default", in ~/.aws/config, relocated by AWS_CONFIG_FILE and
// chosen by AWS_PROFILE (docs/design/bedrock-plumbing.md BR-DIR1, BR-D21). A PROFILE here is the
// file's own word for the name choosing a section — an AWS profile, never a yolo profile.
//
// A PACK FACT, like `region_env_name` beside it, for the reason envoverride.go gives for the
// credential variables (OQ-SSO8): which file an ecosystem's clients read a region from, which
// variable relocates it and which one picks the section are that ecosystem's facts, so core names
// none of them. Core knows only the file's grammar in the abstract, INI sections of `key = value`
// lines (packload's regionfill.go), and the pack says which section and key hold the region.
type RegionFile struct {
	// Path is the file, relative to the home directory of the machine yolo launches on:
	// ".aws/config".
	Path string `json:"path"`
	// PathEnvName names a variable that relocates the file when it is set in the environment yolo
	// was launched from: AWS_CONFIG_FILE. Absent means the file does not move.
	PathEnvName string `json:"path_env_name,omitempty"`
	// ProfileEnvName names the variable whose value, as delivered to the agent, picks the
	// profile: AWS_PROFILE. Absent means only a serving loophole's setting or DefaultProfile does.
	ProfileEnvName string `json:"profile_env_name,omitempty"`
	// DefaultProfile is the profile read when nothing names one, and the one profile with two
	// spellings: `profile_section`'s, `[profile default]`, which takes priority, and its bare
	// name, `[default]`, read only when the file has no section of the first spelling
	// (Sections).
	DefaultProfile string `json:"default_profile"`
	// ProfileSection is every other profile's section header, with `{profile}` standing for the
	// name: "profile {profile}", so profile "dev" is `[profile dev]`.
	ProfileSection string `json:"profile_section"`
	// Key is the key in that section whose value is the region: "region". It is the only key
	// read: a key of another section (an `[sso-session]` block's `sso_region`, the region of the
	// SSO portal rather than of the service) is never looked at, because the profile alone
	// chooses the section.
	Key string `json:"key"`
}

// ProfilePlaceholder is the token in RegionFile.ProfileSection that the profile name replaces.
const ProfilePlaceholder = "{profile}"

// Section is the section header, without brackets, that `profile_section` spells for profile:
// "profile dev" for "dev", and "profile default" for the default profile too.
func (f RegionFile) Section(profile string) string {
	return strings.ReplaceAll(f.ProfileSection, ProfilePlaceholder, profile)
}

// Sections is every section header, without brackets and in priority order, that may hold
// profile's settings: the first the file has is the profile's, and the rest are not read. One
// for most profiles; for the default profile two, `profile_section`'s spelling and then its bare
// name, since that is how both AWS SDKs the shipped agents use read ~/.aws/config: Claude Code's
// bundled JavaScript loader lets `[profile default]` replace `[default]`, and the aws-config
// crate codex links says "profile `[default]` ignored because `[profile default]` was found
// which takes priority" (docs/design/bedrock-plumbing.md BR-D21).
func (f RegionFile) Sections(profile string) []string {
	if profile == f.DefaultProfile {
		return []string{f.Section(profile), f.DefaultProfile}
	}
	return []string{f.Section(profile)}
}

// regionFileProblems validates a provider's `region_file`, and refuses it on every other kind: a
// declaration no consumer reads is the accepted-and-ignored shape this schema refuses everywhere.
// It needs `region_env_name` beside it, because a region read from the file is delivered to the
// agent in the first of the variables that agent reads, so a platform with no variables has
// nowhere to put one.
func regionFileProblems(label string, c Contribution) []string {
	f := c.RegionFile
	if f == nil {
		return nil
	}
	at := label + ".region_file"
	if c.Kind != KindProvider {
		return []string{fmt.Sprintf("%s: kind %q does not take \"region_file\" — it says where a "+
			"PROVIDER's platform keeps a region, so only \"provider\" has one", label, c.Kind)}
	}
	var out []string
	if len(c.RegionEnvName) == 0 {
		out = append(out, at+": needs \"region_env_name\" beside it, the variables a region read "+
			"from the file is delivered in")
	}
	clean := filepath.ToSlash(filepath.Clean(f.Path))
	switch {
	case f.Path == "":
		out = append(out, at+".path: names no file — give the home-relative path, such as \".aws/config\"")
	case filepath.IsAbs(f.Path) || strings.HasPrefix(f.Path, "~") || clean == ".." ||
		strings.HasPrefix(clean, "../") || clean != f.Path:
		out = append(out, fmt.Sprintf("%s.path: %q is not a clean path relative to the home "+
			"directory, such as \".aws/config\"", at, f.Path))
	}
	for _, v := range []struct{ key, name string }{
		{"path_env_name", f.PathEnvName}, {"profile_env_name", f.ProfileEnvName},
	} {
		if v.name != "" && !ValidEnvName(v.name) {
			out = append(out, fmt.Sprintf("%s.%s: invalid env var name %q (must match "+
				"[A-Za-z_][A-Za-z0-9_]*)", at, v.key, v.name))
		}
	}
	if !iniToken(f.DefaultProfile) {
		out = append(out, fmt.Sprintf("%s.default_profile: %q is not a profile name — one token, "+
			"such as \"default\", with no whitespace or brackets", at, f.DefaultProfile))
	}
	if strings.Count(f.ProfileSection, ProfilePlaceholder) != 1 ||
		strings.ContainsAny(f.ProfileSection, "[]\n\r") ||
		strings.TrimSpace(f.ProfileSection) != f.ProfileSection {
		out = append(out, fmt.Sprintf("%s.profile_section: %q is not a section header naming %s "+
			"once, such as \"profile %s\"", at, f.ProfileSection, ProfilePlaceholder, ProfilePlaceholder))
	}
	if !iniToken(f.Key) || strings.Contains(f.Key, "=") {
		out = append(out, fmt.Sprintf("%s.key: %q is not a key name — one token, such as "+
			"\"region\", with no whitespace, brackets or \"=\"", at, f.Key))
	}
	return out
}

// regionProfileSettingProblems validates an env contribution's `region_profile_setting`, and
// refuses it on every other kind. It names a setting of the loophole the contribution is
// `served_by`, so it needs that field; and the profile it yields picks a section of a PLATFORM's
// region file, so it needs a `platform` gate saying which platform's.
func regionProfileSettingProblems(label string, c Contribution) []string {
	if c.RegionProfileSetting == "" {
		return nil
	}
	if c.Kind != KindEnv {
		return []string{fmt.Sprintf("%s: kind %q does not take \"region_profile_setting\" — it says "+
			"which setting of the loophole an \"env\" contribution is served by names the profile "+
			"its credential is for", label, c.Kind)}
	}
	var out []string
	if c.ServedBy == "" {
		out = append(out, label+": \"region_profile_setting\" names a setting of the loophole the "+
			"contribution is served by, so it needs \"served_by\" beside it")
	}
	if c.Platform == "" {
		out = append(out, label+": \"region_profile_setting\" picks a section of a platform's "+
			"region file, so it needs a \"platform\" gate saying which platform")
	}
	if !iniToken(c.RegionProfileSetting) {
		out = append(out, fmt.Sprintf("%s: \"region_profile_setting\" %q is not a setting name",
			label, c.RegionProfileSetting))
	}
	return out
}

// iniToken reports whether s is one non-empty token with no whitespace and no brackets: the
// shape of a profile or key name in a sectioned configuration file.
func iniToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, " \t\r\n[]")
}
