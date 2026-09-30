package packload

// regionfill.go is the REGION FILL (docs/design/bedrock-plumbing.md BR-DIR1, directed
// 2026-09-29: *"Should be the same on the host in the jail, and it should follow that."*): an
// agent on a provider reached through a region, which the composed entry names no region for and
// to which none of the variables it reads its region from is delivered, is given the region its
// platform's REGION FILE holds for the profile its credential comes from — in the first of those
// variables. The region pre-flight (regionpreflight.go) then counts it, since it asks each notch's
// own delivery lookup, and a launch whose file gives none is refused as before, naming the file
// and the profile it read.
//
// ONE PATH, AT EVERY NOTCH (NC-D1; BR-D20): the fill is part of the credential gate's answer
// (ScopeCredentials), appended to the agent's shape vars, so every vehicle already delivers it —
// the jail's per-agent env files, the macos-user session and each agent's file there, the process
// `yolo host` execs — and no vehicle learned a new field. Each notch says only where the file
// lives and what the agent inherits beyond the gate (RegionFileSource).
//
// WHICH FILE, WHICH SECTION, WHICH KEY are the PACK's facts (packdecl.RegionFile; BR-D21):
// packs/bedrock declares ~/.aws/config, relocated by AWS_CONFIG_FILE, the `region` key of
// `[profile NAME]` (for the default profile `[profile default]`, else `[default]`), the profile
// chosen by AWS_PROFILE. Core names no AWS file,
// section or variable here, the OQ-SSO8 rule envoverride.go states.
//
// WHICH PROFILE (BR-D21), in the order the credential's own source decides it:
//  1. a serving loophole's setting, when an `env` contribution declaring
//     `region_profile_setting` reaches the agent (aws-auth's credential pointer): the credential
//     is minted for THAT profile, so its region is the one that belongs to the credential;
//  2. the file's profile variable as delivered to the agent — env_sources at every notch, and at
//     `yolo host` the invoking shell too, which the exec'd agent inherits unless an env_sources
//     null removes it;
//  3. the file's default profile.
//
// PARSED HERE, NEVER BY ASKING `aws` (BR-D22): the file is AWS's documented INI format
// (https://docs.aws.amazon.com/sdkref/latest/guide/file-format.html), a section per profile of
// `key = value` lines, `#` and `;` comments, and indented sub-setting lines under a key, which
// iniValue reads in microseconds, on the launch path, with no dependency on the AWS CLI being
// installed — which it need not be on a macos-user host or at `yolo host`. `aws configure get`
// is a Python start per launch per agent. The same format internal/awsauth's DetectForm reads.
//
// WHAT COUNTS AS "OTHERWISE NONE" mirrors the SDKs' precedence (BR-D23): the provider's own
// `region` first, then a region variable the agent reads that already reaches it, then the file.
// A region variable the agent does NOT read (AWS_DEFAULT_REGION for opencode, BR-D18) does not
// count, so such an agent is given the file's region in the variable it does read. The value
// must be one DNS label (packdecl.RegionProblem), since agents build a host name from it; one
// that is not is never delivered, and the refusal says why.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// RegionFileSource is what one notch tells the region fill (ScopeInput.RegionFiles): where the
// file is read, what the agent inherits beyond the gate, and the configured loophole settings.
// Nil reads no file, which every caller that is not composing a launch passes.
type RegionFileSource struct {
	// Getenv is the environment yolo was launched from. The file's relocation variable
	// (RegionFile.PathEnvName) and HOME are read there, because the file is read on the machine
	// yolo launches on (BR-D24). An empty HOME reads no file.
	Getenv func(string) string
	// Inherited answers a variable the agent inherits beyond what the gate delivers, non-empty:
	// at `yolo host` the invoking shell, which passes through to the exec'd agent, less every
	// name an env_sources null removes from it; and nil in a jail, where no backend forwards
	// that shell (BR-D2).
	Inherited func(string) (string, bool)
	// Setting answers a loophole's configured setting, "" when none is set (LoopholeSettingIn).
	Setting func(loophole, key string) string
}

// RegionFileLookup is what the fill read for one agent: the file, the profile and section it
// chose, and either the region it delivered or why it delivered none.
type RegionFileLookup struct {
	// Var is the variable the region is delivered in: the first the agent reads.
	Var string
	// Region is the region delivered, "" when the file gave none.
	Region string
	// File is the path read, or that would have been read.
	File string
	// Home is the home directory the path was resolved under, for the `~/` a line shows.
	Home    string
	Profile string
	// ProfileFrom says what chose the profile: a loophole setting's config path, the profile
	// variable's name, or "" for the file's default profile.
	ProfileFrom string
	Section     string
	Key         string
	// Problem is why the file gave no region, "" when it gave one.
	Problem string
}

// LoopholeSettingIn answers a loophole's setting from cfg's `loopholes.<name>.settings`, the
// object the launch writes the loophole's settings file from, "" when it holds no string there.
func LoopholeSettingIn(cfg *jsonx.OrderedMap) func(loophole, key string) string {
	return func(loophole, key string) string {
		lp := omapAt(cfg, "loopholes")
		entry := omapAt(lp, loophole)
		settings := omapAt(entry, "settings")
		if settings == nil {
			return ""
		}
		v, _ := settings.Get(key)
		s, _ := v.(string)
		return strings.TrimSpace(s)
	}
}

// omapAt is m's object at key, nil when m is nil or the value is not an object.
func omapAt(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	o, _ := v.(*jsonx.OrderedMap)
	return o
}

// fillRegions runs the fill for every delivery, after the gate has composed each: an agent's
// own delivery is what "a region variable already reaches it" is asked of.
func (s *CredentialScope) fillRegions(in ScopeInput) {
	src := in.RegionFiles
	if src == nil || src.Getenv == nil {
		return
	}
	reqs := regionRequirements(in.Packs)
	files := map[string]fileRead{}
	for _, agent := range s.Agents() {
		d := s.agents[agent]
		if d.Provider == "" {
			continue // a grant-only process selects no provider
		}
		if lookup := s.fillRegion(in, src, reqs, files, d); lookup != nil {
			d.RegionFile = lookup
			if lookup.Region != "" {
				d.Shape = append(d.Shape, agentenv.Var{Key: lookup.Var, Value: lookup.Region})
			}
		}
	}
}

// fileRead is one read of a region file, shared by the agents of one launch.
type fileRead struct {
	data []byte
	err  error
}

// fillRegion is the fill for one delivery: nil when the agent needs nothing from the file (its
// provider is reached through no region, sets one, or declares no region file for its
// platform, or a variable the agent reads already carries one), and otherwise the lookup.
func (s *CredentialScope) fillRegion(in ScopeInput, src *RegionFileSource, reqs map[string]regionRequirement,
	files map[string]fileRead, d *AgentDelivery) *RegionFileLookup {
	entry := providerEntry(in.Providers, d.Provider)
	if entry == nil {
		return nil
	}
	platform := entryString(entry, "platform")
	req, ok := reqs[platform]
	if !ok || req.file == nil || entryString(entry, "region") != "" {
		return nil
	}
	vars, _ := agentRegionVars(in.Packs, d.Agent, platform, req.vars)
	if len(vars) == 0 {
		return nil
	}
	reaches := func(name string) (string, bool) {
		if v, ok := s.DeliveredTo(d.Agent, name); ok {
			return v, true
		}
		if src.Inherited != nil {
			if v, ok := src.Inherited(name); ok && v != "" {
				return v, true
			}
		}
		return "", false
	}
	for _, v := range vars {
		if _, ok := reaches(v); ok {
			return nil
		}
	}
	f := req.file
	l := &RegionFileLookup{Var: vars[0], Key: f.Key}
	// THE PROFILE, in the order the credential's source decides it.
	for _, e := range d.Fold {
		if e.RegionProfileSetting == "" || e.ServedBy == "" || src.Setting == nil {
			continue
		}
		if p := src.Setting(e.ServedBy, e.RegionProfileSetting); p != "" {
			l.Profile, l.ProfileFrom = p, "loopholes."+e.ServedBy+".settings."+e.RegionProfileSetting
			break
		}
	}
	if l.Profile == "" && f.ProfileEnvName != "" {
		if p, ok := reaches(f.ProfileEnvName); ok {
			l.Profile, l.ProfileFrom = strings.TrimSpace(p), f.ProfileEnvName
		}
	}
	if l.Profile == "" {
		l.Profile = f.DefaultProfile
	}
	// THE SECTION: the first of the profile's spellings the file has (RegionFile.Sections). Until
	// the file is read, and when it has none of them, the last — the default profile's bare
	// `[default]`, the spelling a user writes — is the one the refusal's remedy names.
	sections := f.Sections(l.Profile)
	l.Section = sections[len(sections)-1]
	// THE FILE, on the machine yolo launches on.
	l.Home = src.Getenv("HOME")
	if f.PathEnvName != "" {
		if p := src.Getenv(f.PathEnvName); p != "" {
			l.File = expandHome(p, l.Home)
		}
	}
	if l.File == "" {
		if l.Home == "" {
			l.File = "~/" + f.Path
			l.Problem = "not read: the environment yolo was launched from names no home directory"
			return l
		}
		l.File = filepath.Join(l.Home, filepath.FromSlash(f.Path))
	}
	read, cached := files[l.File]
	if !cached {
		read.data, read.err = os.ReadFile(l.File)
		files[l.File] = read
	}
	switch {
	case errors.Is(read.err, fs.ErrNotExist):
		l.Problem = "no such file"
		return l
	case read.err != nil:
		l.Problem = "not read: " + read.err.Error()
		return l
	}
	var value string
	var section, key bool
	for _, sec := range sections {
		if value, section, key = iniValue(read.data, sec, f.Key); section {
			l.Section = sec
			break
		}
	}
	switch {
	case !section:
		l.Problem = "it has no " + sectionList(sections) + " section"
	case !key || value == "":
		l.Problem = "[" + l.Section + "] sets no " + quoted(f.Key)
	case packdecl.RegionProblem("r", value) != "":
		l.Problem = "[" + l.Section + "] sets " + quoted(f.Key) + " to " + quoted(value) +
			", which is not a region (one DNS label, such as \"us-east-1\"), so it was not delivered"
	default:
		l.Region = value
	}
	return l
}

// sectionList names sections as bracketed headers joined by "or": "[default]", "[profile
// default] or [default]".
func sectionList(sections []string) string {
	out := make([]string, len(sections))
	for i, s := range sections {
		out[i] = "[" + s + "]"
	}
	return orList(out)
}

// expandHome expands a leading "~" of p to home, as the SDKs do for a relocated file.
func expandHome(p, home string) string {
	if home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// iniValue reads key in section of an INI-format file: whether the section exists, whether it
// sets the key, and the value, the last setting winning. Section headers are `[name]`, their
// inner whitespace runs collapsed so `[profile  dev]` is `[profile dev]`; a line whose first
// non-blank character is `#` or `;` is a comment; key names compare case-insensitively. A line
// indented past the key before it is that key's continuation or sub-setting (AWS's nested
// `s3 =` block), never a key of the section, which is how configparser, the reader botocore
// uses, reads it.
func iniValue(data []byte, section, key string) (value string, sectionFound, keyFound bool) {
	in := false
	keyIndent := -1
	for _, raw := range strings.Split(string(data), "\n") {
		raw = strings.TrimRight(raw, "\r")
		line := strings.TrimSpace(raw)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			in, keyIndent = false, -1
			if end := strings.IndexByte(line, ']'); end > 0 {
				if strings.Join(strings.Fields(line[1:end]), " ") == section {
					in, sectionFound = true, true
				}
			}
			continue
		}
		if !in {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if keyIndent >= 0 && indent > keyIndent {
			continue // a continuation or sub-setting of the key above
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		keyIndent = indent
		if strings.EqualFold(strings.TrimSpace(k), key) {
			value, keyFound = strings.TrimSpace(v), true
		}
	}
	return value, sectionFound, keyFound
}

// fileLabel is a lookup's file for a line: `~/`-relative under the home it was resolved in.
func (l *RegionFileLookup) fileLabel() string {
	if l.Home != "" && strings.HasPrefix(l.File, l.Home+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(strings.TrimPrefix(l.File, l.Home+string(filepath.Separator)))
	}
	return l.File
}

// profileClause says which profile the lookup read and what chose it.
func (l *RegionFileLookup) profileClause() string {
	switch {
	case strings.HasPrefix(l.ProfileFrom, "loopholes."):
		return "profile " + quoted(l.Profile) + ", the one " + l.ProfileFrom + " names for the credential"
	case l.ProfileFrom != "":
		return "profile " + quoted(l.Profile) + ", from " + l.ProfileFrom
	default:
		return "profile " + quoted(l.Profile) + ", since nothing names another"
	}
}

// refusalFact is the lookup's line under a region refusal: what the file gave, and why that is
// no region.
func (l *RegionFileLookup) refusalFact() string {
	why := l.Problem
	if why == "" {
		// Delivered, and still no region reached the agent: something after the gate removed it
		// (an env_sources null at `yolo host`).
		why = "gave " + quoted(l.Region) + ", delivered as " + l.Var + ", which this launch then removed"
	}
	return "    " + l.fileLabel() + " (" + l.profileClause() + "): " + why
}

// remedy is the file's way to set a region, for the refusal's "set one" line.
func (l *RegionFileLookup) remedy() string {
	return l.Key + " = <region> under [" + l.Section + "] in " + l.fileLabel()
}

// RegionLines is the fill's DISCLOSURE (BR-D25): one line per provider, variable, region, file
// and profile, naming the agents given a region from the file. Nil when the file gave nobody one.
// A disclosure rather than a debug line, so no quiet switch hides it (OQ-RO3); every notch
// prints it beside the gate's own (the jail's noteCredentialScope, the host's disclosure blocks).
func (s *CredentialScope) RegionLines() []string {
	if s == nil {
		return nil
	}
	type group struct {
		provider string
		l        *RegionFileLookup
		agents   []string
	}
	var order []string
	groups := map[string]*group{}
	for _, agent := range s.Agents() {
		d := s.agents[agent]
		l := d.RegionFile
		if l == nil || l.Region == "" {
			continue
		}
		key := strings.Join([]string{d.Provider, l.Var, l.Region, l.File, l.Section, l.ProfileFrom}, "\x00")
		if g, ok := groups[key]; ok {
			g.agents = append(g.agents, agent)
			continue
		}
		groups[key] = &group{provider: d.Provider, l: l, agents: []string{agent}}
		order = append(order, key)
	}
	sort.Strings(order)
	var lines []string
	for _, key := range order {
		g := groups[key]
		lines = append(lines, "Region: "+g.l.Var+"="+g.l.Region+" for "+andList(g.agents)+
			" on provider "+quoted(g.provider)+", read from "+g.l.fileLabel()+" ["+g.l.Section+"] ("+
			g.l.profileClause()+"): the provider sets no region, and no region variable "+
			reachesWhom(g.agents))
	}
	return lines
}

// reachesWhom is "reaches it" for one agent and "reaches them" for several.
func reachesWhom(agents []string) string {
	if len(agents) == 1 {
		return "reaches it"
	}
	return "reaches them"
}
