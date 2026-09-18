package awsauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// settings.go is the CONFIG SURFACE, and the surface is the loophole `settings`
// mechanism — there is no top-level `aws_auth` config key and there must not be one.
//
// # Why not a top-level key
//
// OQ-SSO4 ruled the profile, the role and the session policy USER-CONFIG ONLY: a
// workspace yolo-jail.jsonc is a file the agent inside the jail can rewrite, so an
// agent that could edit it could otherwise point this service at the `admin` profile.
// `internal/loopholedecl/settings.go` already has exactly that vocabulary — a per-key
// `scope` defaulting to `user`, type-checked, validated host-side, written to a flat
// 0600 JSON file the daemon's argv names through the `{settings}` token. A top-level
// key would be a second scope grammar for one feature, which is the thing that ruling
// refuses.
//
// So THIS file reads the file yolo wrote. It never reads a yolo-jail.jsonc, never
// reads an environment variable, and never takes any of the three from a request: the
// jail does not get to name the profile, role or policy it wants.
//
// # Absence never means un-narrowed
//
// OQ-SSO1: a narrowing scope is REQUIRED by default and un-narrowed is available only
// when asked for BY NAME. That is a property of the key names as much as of the code —
// every key here defaults to the zero that refuses, and the one key that widens is a
// BOOL, for the reason packs/journal's manifest states at length: the settings type set
// is closed with no `enum`, so core cannot refuse a misspelled string, and a typo must
// never be the spelling that grants. A bool cannot spell itself wrong.

// Setting key names, as declared in the loophole manifest under
// `loopholes.aws-auth.settings`. Spelled as constants because the refusals below
// have to name them and a literal in two places is a literal that drifts.
const (
	// SettingProfile names the AWS profile this service resolves. No default:
	// absent means the service has nothing to serve and refuses at spawn.
	SettingProfile = "profile"
	// SettingRoleARN is the role the N2/N3 arms assume.
	SettingRoleARN = "role_arn"
	// SettingSessionPolicy is the inline session policy JSON the N2 arm attaches.
	SettingSessionPolicy = "session_policy"
	// SettingUnnarrowed is the ONE widening key: serve the permission set as-is.
	// Bool, default false, disclosed at every launch when true.
	SettingUnnarrowed = "unnarrowed"
)

// settingsScope is how a refusal spells a key for the human: the full config path
// they would edit.
func settingsScope(key string) string { return "loopholes.aws-auth.settings." + key }

// Settings is the flat file `{settings}` names, decoded. Every declared key is
// present in that file (loopholedecl guarantees totality), so absence here means
// only "an older or hand-written file", and each field's zero is the refusing one.
type Settings struct {
	Profile       string `json:"profile"`
	RoleARN       string `json:"role_arn"`
	SessionPolicy string `json:"session_policy"`
	Unnarrowed    bool   `json:"unnarrowed"`
}

// LoadSettings reads the resolved settings file.
//
// A MISSING path and a MISSING file are different from an UNPARSEABLE one, and the
// caller needs the difference: no path means the daemon was run by hand, no file means
// no jail has launched this loophole yet (the normal state of a fresh machine), and a
// file that does not parse is a real fault. Only the last is an error here.
func LoadSettings(path string) (Settings, error) {
	if path == "" {
		return Settings{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{}, nil
		}
		return Settings{}, fmt.Errorf("read aws-auth settings: %w", err)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("decode aws-auth settings at %s: %w", path, err)
	}
	return s, nil
}

// NarrowingKind is which of the design's narrowing arms is in force.
type NarrowingKind string

const (
	// NarrowSessionPolicy is N2: AssumeRole plus an inline session policy. The
	// cheapest arm that narrows INSIDE Bedrock.
	NarrowSessionPolicy NarrowingKind = "session-policy"
	// NarrowRole is N3: a purpose-built role, assumed, with no extra policy.
	NarrowRole NarrowingKind = "role"
	// NarrowNone is un-narrowed, asked for by name: serve whatever the permission
	// set grants. Covers both "my permission set is already narrow enough" (N4, and
	// the row people miss — pointing the profile at a narrower permission set you
	// are ALREADY assigned) and "I have nothing to narrow with yet". Never the
	// result of an absent key.
	NarrowNone NarrowingKind = "none"
)

// Narrowing is the resolved narrowing decision. Zero value is not servable — use
// Resolve.
type Narrowing struct {
	Kind          NarrowingKind
	RoleARN       string
	SessionPolicy string
}

// Digest fingerprints the narrowing, so a cache entry minted under a different
// configuration reads as a miss. It covers the policy TEXT, because editing the
// policy without editing the role must invalidate the cache.
func (n Narrowing) Digest() string {
	return Fingerprint(string(n.Kind) + "\x00" + n.RoleARN + "\x00" + n.SessionPolicy)
}

// DisclosureLine is the one line a launch prints when this service will serve
// UN-NARROWED credentials, and "" when it will not.
//
// OQ-SSO1's second half: "the explicit setting is disclosed at every launch". A
// widening that is silent after the first read is a widening nobody re-consents to,
// which is why this is a function of the RESOLVED values rather than a comment in a
// manifest. The daemon prints it at spawn and grades it as a NOTE in its self-check;
// a launch-side caller has the resolved values in hand in writeLoopholeSettings.
func (n Narrowing) DisclosureLine(profile string) string {
	if n.Kind != NarrowNone {
		return ""
	}
	return fmt.Sprintf("aws-auth: serving UN-NARROWED credentials for profile %q — the jail "+
		"holds whatever that permission set grants (%s is true)",
		profile, settingsScope(SettingUnnarrowed))
}

// Describe is the one-line summary of what is in force, for a startup line and for
// the self-check. It names no secret and no policy body.
func (n Narrowing) Describe() string {
	switch n.Kind {
	case NarrowSessionPolicy:
		return "AssumeRole " + n.RoleARN + " with an inline session policy (" +
			Fingerprint(n.SessionPolicy) + ")"
	case NarrowRole:
		return "AssumeRole " + n.RoleARN + " with no additional session policy"
	case NarrowNone:
		return "none — the permission set is served as-is"
	default:
		return "unresolved"
	}
}

// Config is a servable configuration: a profile and a narrowing. Producing one is
// the only way to reach a mint.
type Config struct {
	Profile   string
	Narrowing Narrowing
}

// Resolve turns settings into a servable Config, or REFUSES and says which key to
// write. Every refusal names a full config path, because the person reading it is
// about to edit a file.
//
// # This is the step the design calls expensive if late
//
// Requiring the narrowing is cheap now and breaking later: a default that served
// un-narrowed credentials and was tightened afterwards would break every setup that
// had come to depend on it, so the widening has to be explicit from the first
// release. Read the refusals in that order — the absent case is the one that matters.
func (s Settings) Resolve() (Config, error) {
	profile := strings.TrimSpace(s.Profile)
	if profile == "" {
		return Config{}, fmt.Errorf("no AWS profile is configured: set %s in your USER config "+
			"(~/.config/yolo-jail/config.jsonc) to the profile this service should resolve. It is "+
			"user-scope on purpose — a workspace yolo-jail.jsonc is a file the jail's own agent can "+
			"rewrite", settingsScope(SettingProfile))
	}
	role := strings.TrimSpace(s.RoleARN)
	policy := strings.TrimSpace(s.SessionPolicy)

	// A POLICY WITH NOTHING TO ATTACH IT TO is refused rather than ignored. An
	// inline session policy is an argument to AssumeRole; with no role there is no
	// call to attach it to, so honouring the half that parsed would serve a credential
	// the user believes is policy-narrowed and is not.
	if policy != "" && role == "" {
		return Config{}, fmt.Errorf("%s is set but %s is not: an inline session policy is an "+
			"argument to AssumeRole, so without a role there is nothing to attach it to and the "+
			"credential would be served un-narrowed",
			settingsScope(SettingSessionPolicy), settingsScope(SettingRoleARN))
	}

	// BOTH ARMS AT ONCE cannot be resolved in the direction that grants. Preferring
	// the role would silently give someone who asked for un-narrowed something
	// narrower (confusing); preferring un-narrowed would silently discard a narrowing
	// they configured (unsafe). Neither is a guess worth making.
	if s.Unnarrowed && role != "" {
		return Config{}, fmt.Errorf("%s is true AND %s is set — drop one: the role is a narrowing "+
			"and %s asks for none, so which wins is not something this service should guess",
			settingsScope(SettingUnnarrowed), settingsScope(SettingRoleARN),
			settingsScope(SettingUnnarrowed))
	}

	switch {
	case role != "" && policy != "":
		if err := validPolicyJSON(policy); err != nil {
			return Config{}, fmt.Errorf("%s is not a JSON policy document: %w — STS would refuse "+
				"every mint, so this is refused at spawn instead",
				settingsScope(SettingSessionPolicy), err)
		}
		return Config{Profile: profile, Narrowing: Narrowing{
			Kind: NarrowSessionPolicy, RoleARN: role, SessionPolicy: policy,
		}}, nil
	case role != "":
		return Config{Profile: profile, Narrowing: Narrowing{Kind: NarrowRole, RoleARN: role}}, nil
	case s.Unnarrowed:
		return Config{Profile: profile, Narrowing: Narrowing{Kind: NarrowNone}}, nil
	default:
		// THE REFUSAL OQ-SSO1 IS. Absence is never un-narrowed.
		return Config{}, fmt.Errorf("no narrowing is configured for AWS profile %q: set %s to a "+
			"role this service should assume (add %s to narrow inside Bedrock), or set %s to true "+
			"to serve that permission set as-is. Serving it as-is is available and is never the "+
			"default: a credential this service mints is readable by every process in the jail, so "+
			"the narrowing is the only defence and it has to be asked for",
			profile, settingsScope(SettingRoleARN), settingsScope(SettingSessionPolicy),
			settingsScope(SettingUnnarrowed))
	}
}

// validPolicyJSON checks the session policy parses as a JSON object. It does NOT
// validate the policy language: STS owns that vocabulary and forwarding STS's own
// refusal is better than a second, staler grammar here. What it catches is the whole
// class of shell-quoting and heredoc accidents, at spawn rather than per request.
func validPolicyJSON(policy string) error {
	var doc map[string]any
	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		return err
	}
	if len(doc) == 0 {
		return errors.New("it is an empty object")
	}
	if _, ok := doc["Statement"]; !ok {
		keys := make([]string, 0, len(doc))
		for k := range doc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Errorf("it has no \"Statement\" key (found: %s)", strings.Join(keys, ", "))
	}
	return nil
}
