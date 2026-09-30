package packload

// regionfill_test.go pins THE REGION FILL (regionfill.go; docs/design/bedrock-plumbing.md
// BR-DIR1 and BR-D20 to BR-D25) through the credential gate every vehicle reads
// (ScopeCredentials), over the shipped packs, so the facts packs/bedrock and packs/aws-auth
// declare are the ones under test: which profile's region an agent is given, in which variable,
// when it is given none, and what the refusal and the disclosure say. The notches that hand the
// gate a RegionFileSource are pinned where they compose: internal/cli/run
// (regionfilljail_test.go) and internal/cli (hostregionfile_test.go).
//
// Every fixture is an invented config under a temp home, handed to the fill through the source's
// own Getenv: no test reads the machine's ~/.aws.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// regionConfig is an invented AWS config: a default profile, an SSO profile whose sso-session
// names a portal region of its own, a second profile, a profile with a region only in a nested
// sub-setting, one whose only region-like key is the session's sso_region, and one whose region
// is not a region.
const regionConfig = `# an invented AWS config
[default]
region = us-east-2

[profile  team-sso]
sso_session = portal
sso_account_id = 000000000000
REGION = eu-west-1

[sso-session portal]
sso_region = ap-southeast-2
sso_start_url = https://example.invalid/start
region = sa-east-1

[profile other]
; a comment
region = ca-central-1
region = ca-west-1

[profile nested]
s3 =
  region = us-west-1

[profile sso-only]
sso_session = portal

[profile hostile]
region = eu-west-1.attacker.example
`

// regionHome writes body as <home>/.aws/config and returns home.
func regionHome(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".aws"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".aws", "config"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// launchEnv is a launching environment holding vars and nothing else.
func launchEnv(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// fillCase is one gate composition with a region source.
type fillCase struct {
	packs      []*Pack
	user       string // the user's `providers` block, "" for none
	profiles   map[string]string
	envSources *jsonx.OrderedMap
	served     *ServedDaemons // nil serves every daemon as declared
	src        *RegionFileSource
}

func (c fillCase) scope(t *testing.T) (*CredentialScope, *jsonx.OrderedMap) {
	t.Helper()
	var user *jsonx.OrderedMap
	if c.user != "" {
		user = userProviders(t, c.user)
	}
	providers, resolved, _ := launchSelection(t, c.packs, user, nil, c.profiles)
	s, err := ScopeCredentials(ScopeInput{Packs: c.packs, Providers: providers, Profiles: c.profiles,
		Resolved: resolved, EnvSources: c.envSources, Served: c.served, CallerTokens: awsToken,
		RegionFiles: c.src})
	if err != nil {
		t.Fatalf("the gate refused: %v", err)
	}
	return s, providers
}

// shapeValue is the value the delivery's shape vars set for key, "" when none does.
func shapeValue(d *AgentDelivery, key string) string {
	out := ""
	if d == nil {
		return out
	}
	for _, v := range d.Shape {
		if v.Key == key && !v.Unset {
			out = v.Value
		}
	}
	return out
}

// awsAuthSetting answers aws-auth's configured profile through LoopholeSettingIn, over a config
// that sets it, as a launch's config does.
func awsAuthSetting(profile string) func(string, string) string {
	cfg := jsonx.NewOrderedMap()
	settings := jsonx.NewOrderedMap()
	settings.Set("profile", profile)
	entry := jsonx.NewOrderedMap()
	entry.Set("settings", settings)
	loopholes := jsonx.NewOrderedMap()
	loopholes.Set("aws-auth", entry)
	cfg.Set("loopholes", loopholes)
	return LoopholeSettingIn(cfg)
}

// WHICH PROFILE: the one the credential is minted for when aws-auth serves the agent, else the
// AWS_PROFILE the agent receives, else the default — and the region is its OWN section's key,
// never an sso-session's sso_region.
func TestTheRegionFillReadsTheProfileTheCredentialComesFrom(t *testing.T) {
	home := regionHome(t, regionConfig)
	packs := embeddedNamed(t, "claude", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	onBedrock := map[string]string{"claude": "bedrock"}
	nothing := NothingServed()
	jailServed := ServedInJail([]string{"aws-auth", "wire-bridge"}).WithListen(declaredListen)
	for _, tc := range []struct {
		name        string
		env         *jsonx.OrderedMap
		served      *ServedDaemons
		setting     string
		wantRegion  string
		wantFrom    string
		wantProfile string
	}{
		{"aws-auth serves the agent: its configured profile", nil, &jailServed, "team-sso",
			"eu-west-1", "loopholes.aws-auth.settings.profile", "team-sso"},
		{"aws-auth serves it, over an AWS_PROFILE the agent receives", hydrated("AWS_PROFILE", "other"),
			&jailServed, "team-sso", "eu-west-1", "loopholes.aws-auth.settings.profile", "team-sso"},
		{"aws-auth does not serve it: the AWS_PROFILE it receives", hydrated("AWS_PROFILE", "other"),
			&nothing, "team-sso", "ca-west-1", "AWS_PROFILE", "other"},
		{"aws-auth serves it with no profile configured: the AWS_PROFILE it receives",
			hydrated("AWS_PROFILE", "other"), &jailServed, "", "ca-west-1", "AWS_PROFILE", "other"},
		{"nothing names a profile: the file's default", nil, &nothing, "", "us-east-2", "", "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := fillCase{packs: packs, profiles: onBedrock, envSources: tc.env, served: tc.served,
				src: &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": home}),
					Setting: awsAuthSetting(tc.setting)}}.scope(t)
			d := s.Agent("claude")
			if got := shapeValue(d, "AWS_REGION"); got != tc.wantRegion {
				t.Errorf("claude was given AWS_REGION=%q, want %q (lookup %+v)", got, tc.wantRegion, d.RegionFile)
			}
			if l := d.RegionFile; l == nil || l.ProfileFrom != tc.wantFrom || l.Profile != tc.wantProfile {
				t.Errorf("the lookup read %+v, want profile %q from %q", l, tc.wantProfile, tc.wantFrom)
			}
		})
	}

	// NEVER THE PORTAL'S REGION: a profile whose sso-session names sso_region, and which sets no
	// region of its own, gets nothing from the session block — nor from a `region` key there.
	s, _ := fillCase{packs: packs, profiles: onBedrock, served: &nothing,
		envSources: hydrated("AWS_PROFILE", "sso-only"),
		src:        &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": home})}}.scope(t)
	d := s.Agent("claude")
	if got := shapeValue(d, "AWS_REGION"); got != "" {
		t.Errorf("a profile with no region of its own was given %q, read from its sso-session block", got)
	}
	if d.RegionFile == nil || !strings.Contains(d.RegionFile.Problem, `[profile sso-only] sets no "region"`) {
		t.Errorf("the lookup must say the profile's own section sets no region: %+v", d.RegionFile)
	}
}

// WHEN THE AGENT HAS A REGION, the file is not read for it: the provider's own `region`, and a
// region variable it reads that reaches it — from env_sources at every notch, and at `yolo host`
// from the invoking shell it inherits (Inherited). A region only in a jail's launching shell is
// no region of the agent's, so the file still fills it.
func TestTheRegionFillFillsOnlyAnAgentWithNoRegion(t *testing.T) {
	home := regionHome(t, regionConfig)
	packs := embeddedNamed(t, "claude", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	onBedrock := map[string]string{"claude": "bedrock"}
	nothing := NothingServed()
	src := func(inherited map[string]string) *RegionFileSource {
		s := &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": home, "AWS_REGION": "us-west-2"})}
		if inherited != nil {
			s.Inherited = func(name string) (string, bool) { v, ok := inherited[name]; return v, ok }
		}
		return s
	}
	for _, tc := range []struct {
		name, user string
		env        *jsonx.OrderedMap
		inherited  map[string]string
		want       string // AWS_REGION claude receives
		filled     bool
	}{
		{"the provider's region", `{"bedrock":{"region":"ap-south-1"}}`, nil, nil, "ap-south-1", false},
		{"AWS_REGION from env_sources", "", hydrated("AWS_REGION", "eu-central-1"), nil, "", false},
		{"AWS_DEFAULT_REGION from env_sources, which claude reads", "", hydrated("AWS_DEFAULT_REGION", "eu-central-1"), nil, "", false},
		{"AWS_REGION in the shell the agent inherits", "", nil, map[string]string{"AWS_REGION": "eu-north-1"}, "", false},
		{"an empty AWS_REGION in that shell is no region", "", nil, map[string]string{"AWS_REGION": ""}, "us-east-2", true},
		{"a region only in a jail's launching shell", "", nil, nil, "us-east-2", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := fillCase{packs: packs, user: tc.user, profiles: onBedrock, envSources: tc.env,
				served: &nothing, src: src(tc.inherited)}.scope(t)
			d := s.Agent("claude")
			if got := shapeValue(d, "AWS_REGION"); got != tc.want {
				t.Errorf("claude's shape AWS_REGION = %q, want %q", got, tc.want)
			}
			if filled := d.RegionFile != nil; filled != tc.filled {
				t.Errorf("the file was consulted = %v, want %v (%+v)", filled, tc.filled, d.RegionFile)
			}
		})
	}
}

// IN THE VARIABLE THE AGENT READS (BR-D18): opencode reads AWS_REGION alone, so an
// AWS_DEFAULT_REGION it receives is no region of its, and it is given the file's region as
// AWS_REGION — where claude, which reads AWS_DEFAULT_REGION, is given nothing.
func TestTheRegionFillDeliversInTheVariableTheAgentReads(t *testing.T) {
	home := regionHome(t, regionConfig)
	packs := embeddedNamed(t, "claude", "opencode", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	nothing := NothingServed()
	s, _ := fillCase{packs: packs, profiles: map[string]string{"claude": "bedrock", "opencode": "bedrock"},
		envSources: hydrated("AWS_DEFAULT_REGION", "eu-central-1"), served: &nothing,
		src: &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": home})}}.scope(t)
	if got := shapeValue(s.Agent("opencode"), "AWS_REGION"); got != "us-east-2" {
		t.Errorf("opencode, which ignores AWS_DEFAULT_REGION, was given AWS_REGION=%q, want the file's us-east-2", got)
	}
	if d := s.Agent("claude"); shapeValue(d, "AWS_REGION") != "" || d.RegionFile != nil {
		t.Errorf("claude reads AWS_DEFAULT_REGION, so it needs nothing from the file: %+v", d.RegionFile)
	}
}

// THE FILE: AWS_CONFIG_FILE in the launching environment relocates it (a leading ~ expanded
// under that environment's HOME); a missing file, a missing section, no HOME and a value that is
// not one DNS label each deliver nothing and say why.
func TestTheRegionFillReadsTheFileTheLaunchNames(t *testing.T) {
	packs := embeddedNamed(t, "claude", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	onBedrock := map[string]string{"claude": "bedrock"}
	nothing := NothingServed()
	home := regionHome(t, "[default]\nregion = us-east-2\n")
	if err := os.WriteFile(filepath.Join(home, "elsewhere"), []byte("[default]\nregion = me-central-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		env     map[string]string
		profile string
		want    string
		problem string
	}{
		{"AWS_CONFIG_FILE relocates it", map[string]string{"HOME": home, "AWS_CONFIG_FILE": filepath.Join(home, "elsewhere")},
			"", "me-central-1", ""},
		{"a leading ~ in AWS_CONFIG_FILE is the home", map[string]string{"HOME": home, "AWS_CONFIG_FILE": "~/elsewhere"},
			"", "me-central-1", ""},
		{"a missing file", map[string]string{"HOME": home, "AWS_CONFIG_FILE": filepath.Join(home, "absent")},
			"", "", "no such file"},
		{"a profile the file does not hold", map[string]string{"HOME": home}, "ghost", "", "it has no [profile ghost] section"},
		{"no home directory", map[string]string{}, "", "", "names no home directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env *jsonx.OrderedMap
			if tc.profile != "" {
				env = hydrated("AWS_PROFILE", tc.profile)
			}
			s, _ := fillCase{packs: packs, profiles: onBedrock, envSources: env, served: &nothing,
				src: &RegionFileSource{Getenv: launchEnv(tc.env)}}.scope(t)
			d := s.Agent("claude")
			if got := shapeValue(d, "AWS_REGION"); got != tc.want {
				t.Errorf("AWS_REGION = %q, want %q", got, tc.want)
			}
			if d.RegionFile == nil || !strings.Contains(d.RegionFile.Problem, tc.problem) {
				t.Errorf("the lookup's problem must say %q: %+v", tc.problem, d.RegionFile)
			}
		})
	}

	// A REGION IS ONE DNS LABEL: the agent builds its host name from it, so a value that is not
	// one is never delivered, whoever wrote the file.
	hostile := regionHome(t, regionConfig)
	s, _ := fillCase{packs: packs, profiles: onBedrock, envSources: hydrated("AWS_PROFILE", "hostile"),
		served: &nothing, src: &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": hostile})}}.scope(t)
	d := s.Agent("claude")
	if got := shapeValue(d, "AWS_REGION"); got != "" {
		t.Errorf("a region that is a host name was delivered: %q", got)
	}
	if d.RegionFile == nil || !strings.Contains(d.RegionFile.Problem, "which is not a region") {
		t.Errorf("the lookup must say the value is not a region: %+v", d.RegionFile)
	}

	// NO SOURCE READS NOTHING: a caller composing no launch (`yolo check`) passes none.
	s, _ = fillCase{packs: packs, profiles: onBedrock, served: &nothing}.scope(t)
	if d := s.Agent("claude"); d.RegionFile != nil || shapeValue(d, "AWS_REGION") != "" {
		t.Errorf("with no region source nothing is read: %+v", d.RegionFile)
	}
}

// THE PRE-FLIGHT COUNTS IT AND NAMES IT (BR-D25): a filled region reaches the agent's delivery,
// which is what every notch's lookup asks, so the launch is not refused; a file that gives none
// is named under the refusal, with the profile, the reason and a third way to set a region, and
// the consulted line ends with it.
func TestTheRegionPreflightCountsAFilledRegionAndNamesTheFileItRead(t *testing.T) {
	packs := embeddedNamed(t, "claude", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	onBedrock := map[string]string{"claude": "bedrock"}
	nothing := NothingServed()
	ask := func(s *CredentialScope) []RegionAsk {
		d := s.Agent("claude")
		return []RegionAsk{{Agent: "claude", Provider: d.Provider, File: d.RegionFile,
			Lookup: func(name string) (string, bool) { return s.DeliveredTo("claude", name) }}}
	}

	home := regionHome(t, regionConfig)
	s, providers := fillCase{packs: packs, profiles: onBedrock, served: &nothing,
		src: &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": home})}}.scope(t)
	if facts := ProviderRegionGaps(packs, providers, ask(s), nil, nil); facts != nil {
		t.Errorf("a region the file gave must satisfy the pre-flight:\n%s", strings.Join(facts, "\n"))
	}

	bare := regionHome(t, "[profile other]\nregion = ca-central-1\n")
	s, providers = fillCase{packs: packs, profiles: onBedrock, served: &nothing,
		src: &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": bare})}}.scope(t)
	facts := ProviderRegionGaps(packs, providers, ask(s), nil, RegionConsulted(nil, FromPackEnv))
	got := strings.Join(facts, "\n")
	for _, want := range []string{
		`    ~/.aws/config (profile "default", since nothing names another): it has no [default] section`,
		`or AWS_REGION=<region> in an env_sources entry, or region = <region> under [default] in ~/.aws/config`,
		"consulted for a region: each selected provider's composed entry, then " + FromEnvSources +
			": none configured, " + FromPackEnv + ", then ~/.aws/config [default]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the facts must say %q:\n%s", want, got)
		}
	}
}

// THE DISCLOSURE (BR-D25): one line per provider, variable, region, file and profile, naming the
// agents given it, and what chose the profile.
func TestTheRegionFillDisclosesTheRegionTheFileAndTheProfile(t *testing.T) {
	home := regionHome(t, regionConfig)
	packs := embeddedNamed(t, "claude", "opencode", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	jailServed := ServedInJail([]string{"aws-auth", "wire-bridge"}).WithListen(declaredListen)
	s, _ := fillCase{packs: packs, profiles: map[string]string{"claude": "bedrock", "opencode": "bedrock"},
		served: &jailServed, src: &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": home}),
			Setting: awsAuthSetting("team-sso")}}.scope(t)
	want := `Region: AWS_REGION=eu-west-1 for claude and opencode on provider "bedrock", read from ` +
		`~/.aws/config [profile team-sso] (profile "team-sso", the one loopholes.aws-auth.settings.profile ` +
		`names for the credential): the provider sets no region, and no region variable reaches them`
	if got := strings.Join(s.RegionLines(), "\n"); got != want {
		t.Errorf("the disclosure is\n%s\nwant\n%s", got, want)
	}
	nothing := NothingServed()
	s, _ = fillCase{packs: packs, profiles: map[string]string{"claude": "bedrock"}, served: &nothing,
		src: &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": home})}}.scope(t)
	if got := strings.Join(s.RegionLines(), "\n"); !strings.HasSuffix(got,
		`[default] (profile "default", since nothing names another): the provider sets no region, and no region variable reaches it`) {
		t.Errorf("the default profile's disclosure is %q", got)
	}
	s, _ = fillCase{packs: packs, profiles: map[string]string{"claude": "bedrock"},
		user: `{"bedrock":{"region":"us-west-2"}}`, served: &nothing,
		src: &RegionFileSource{Getenv: launchEnv(map[string]string{"HOME": home})}}.scope(t)
	if lines := s.RegionLines(); lines != nil {
		t.Errorf("nothing filled, nothing disclosed: %v", lines)
	}
}

// THE FORMAT (BR-D22): AWS's documented INI shape, read the way configparser, botocore's reader,
// reads it — collapsed header whitespace, full-line comments, case-insensitive keys, the last
// setting winning, CRLF endings, and a nested sub-setting's lines never read as the section's own.
func TestIniValueReadsTheSharedConfigFormat(t *testing.T) {
	data := []byte(regionConfig + "\r\n[profile crlf]\r\nregion = us-gov-west-1\r\n")
	for _, tc := range []struct {
		section, want string
		section2, key bool
	}{
		{"default", "us-east-2", true, true},
		{"profile team-sso", "eu-west-1", true, true},   // `[profile  team-sso]`, `REGION`
		{"profile other", "ca-west-1", true, true},      // the later setting wins
		{"profile nested", "", true, false},             // only `s3`'s sub-setting names a region
		{"profile crlf", "us-gov-west-1", true, true},   // CRLF endings
		{"sso-session portal", "sa-east-1", true, true}, // readable only by naming the section
		{"profile absent", "", false, false},
	} {
		v, section, key := iniValue(data, tc.section, "region")
		if v != tc.want || section != tc.section2 || key != tc.key {
			t.Errorf("[%s] region = %q (section %v, key %v), want %q (%v, %v)",
				tc.section, v, section, key, tc.want, tc.section2, tc.key)
		}
	}
}

// THE SHIPPED FACTS: packs/bedrock declares AWS's region file for "aws-bedrock", and packs/aws-auth
// says its pointer's credential is minted for its `profile` setting, which its loophole declares.
// Read from the embedded packs, so deleting either declaration fails here.
func TestTheShippedPacksDeclareTheBedrockRegionFile(t *testing.T) {
	reqs := regionRequirements(embeddedNamed(t, "bedrock"))
	f := reqs["aws-bedrock"].file
	if f == nil {
		t.Fatal(`packs/bedrock declares no region_file for "aws-bedrock"`)
	}
	if f.Path != ".aws/config" || f.PathEnvName != "AWS_CONFIG_FILE" || f.ProfileEnvName != "AWS_PROFILE" ||
		f.DefaultProfile != "default" || f.Section("dev") != "profile dev" || f.Section("default") != "default" ||
		f.Key != "region" {
		t.Errorf("packs/bedrock's region_file is %+v, want AWS's documented ~/.aws/config grammar", *f)
	}
	awsAuth := embeddedNamed(t, "aws-auth")[0]
	var setting string
	for _, g := range awsAuth.Decl.GatedEnvContributions() {
		if g.ServedBy == "aws-auth" && g.Platform == "aws-bedrock" {
			setting = g.RegionProfileSetting
		}
	}
	if setting != "profile" {
		t.Errorf("aws-auth's pointer names region_profile_setting %q, want its loophole's \"profile\"", setting)
	}
}
