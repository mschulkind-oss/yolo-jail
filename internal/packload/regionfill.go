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
// chosen by AWS_PROFILE. Core names no AWS file, section or variable here, the OQ-SSO8 rule
// envoverride.go states.
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
// `region` first, then a region variable that already reaches the agent, then the file. A region
// variable the agent does NOT read (AWS_DEFAULT_REGION for opencode, BR-D18) is no region of its,
// and no license to put the file's in its place either: the user set that one for this launch,
// so the fill leaves the agent to the pre-flight, which refuses naming the unread variable. The
// value must be one DNS label (packdecl.RegionProblem), since agents build a host name from it;
// one that is not is never delivered, and the refusal says why.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	// Stranded reports whether a variable is set, non-empty, in the environment yolo was
	// launched from, asked only of one that reaches the agent by no channel: in a jail, whose
	// agent no backend hands that shell (BR-D2), a region variable or the profile variable left
	// there is a choice the user made for this launch that it does not carry, and the fill does
	// not replace it with the file's answer for another profile — the launch is refused, naming
	// it. Nil at `yolo host`, where that shell is Inherited.
	Stranded func(string) bool
	// Setting answers a loophole's configured setting, "" when none is set (LoopholeSettingIn).
	Setting func(loophole, key string) string
}

// RegionFileLookup is what the fill read for one agent: the file, the profile and section it
// chose, and either the region it delivered or why it delivered none.
type RegionFileLookup struct {
	// Provider is the provider the fill read the file for: the agent's primary, or the first
	// entry of its active set that needed a region (docs/design/active-provider-sets.md AP-P1).
	Provider string
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
	// FromParent is the variable of the environment yolo was launched from that the region was
	// taken from, in place of the file, when the agent's credential pointer is inherited from the
	// launching jail (ServedDaemons.WithInherited): the region that came with the credential
	// (SSO-D4). "" when the region, or its absence, is the file's.
	FromParent string
	// stranded is the variable, set where yolo was launched and delivered to the agent by no
	// channel, that kept the file from being read (RegionFileSource.Stranded), "" when none did;
	// strandedProfile is the profile it names when it is the profile variable.
	stranded, strandedProfile string
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
//
// OVER THE AGENT'S ACTIVE SET (docs/design/active-provider-sets.md AP-P1): the first entry, in
// set order, whose provider needs a region from the file gets the fill, so a Bedrock entry after
// the first reads its region as a primary one does. One lookup per agent: a set names a regional
// platform once (AP-D12), and a second platform needing the file would be refused by the region
// pre-flight, which names what it asked, rather than filled in silence.
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
		for _, provider := range d.setProviders() {
			lookup := s.fillRegion(in, src, reqs, files, d, provider)
			if lookup == nil {
				continue
			}
			d.RegionFile = lookup
			if lookup.Region != "" {
				d.Shape = append(d.Shape, agentenv.Var{Key: lookup.Var, Value: lookup.Region})
			}
			break
		}
	}
}

// RegionFileFor is what the region fill read for provider in d's active set, nil when it read
// nothing for it: the lookup the region pre-flight's ask for that provider carries (RegionAsk.File).
func (d *AgentDelivery) RegionFileFor(provider string) *RegionFileLookup {
	if d == nil || d.RegionFile == nil || d.RegionFile.Provider != provider {
		return nil
	}
	return d.RegionFile
}

// fileRead is one read of a region file, shared by the agents of one launch.
type fileRead struct {
	data []byte
	err  error
}

// fillRegion is the fill for one delivery's provider: nil when the agent needs nothing from the
// file for it (the provider is reached through no region, sets one, or declares no region file
// for its platform, or a variable the agent reads already carries one), and otherwise the lookup.
func (s *CredentialScope) fillRegion(in ScopeInput, src *RegionFileSource, reqs map[string]regionRequirement,
	files map[string]fileRead, d *AgentDelivery, provider string) *RegionFileLookup {
	entry := providerEntry(in.Providers, provider)
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
	// A REGION ALREADY THERE: any region variable reaching the agent, the platform's or its own,
	// read or not. One it reads is its region. One it does not read (AWS_DEFAULT_REGION for
	// opencode, BR-D18) is still a region the user chose for this launch, so the file's, which may
	// differ, is not put in its place; the pre-flight refuses, naming the variable unread.
	regionVars := append(slices.Clone(req.vars), vars...)
	for _, v := range regionVars {
		if _, ok := reaches(v); ok {
			return nil
		}
	}
	f := req.file
	// A REGION THAT CAME WITH THE CREDENTIAL (SSO-D4): an agent whose pointer this launch takes
	// from the launching jail uses the launching jail's credential, so the region that belongs to
	// it is the one the launching jail's own agents use, set where yolo was launched — the rule
	// WHICH PROFILE's first step states for a minted credential. Read before the file, which
	// names a profile this launch's credential was never minted for.
	if region, from := s.inheritedRegion(src, d, regionVars); region != "" {
		return &RegionFileLookup{Provider: provider, Var: vars[0], Region: region, Key: f.Key, FromParent: from}
	}
	l := &RegionFileLookup{Provider: provider, Var: vars[0], Key: f.Key}
	// A REGION LEFT WHERE YOLO WAS LAUNCHED, which this launch does not deliver (a jail's shell,
	// BR-D2): the region the user chose, which the file's may not be, so the file is not read.
	if src.Stranded != nil {
		for _, v := range regionVars {
			if src.Stranded(v) {
				l.stranded = v
				l.Problem = v + ", set in the environment yolo was launched from, names the " +
					"region you chose, which the file's may not be"
				break
			}
		}
	}
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
		} else if p := strings.TrimSpace(src.Getenv(f.ProfileEnvName)); l.stranded == "" &&
			src.Stranded != nil && src.Stranded(f.ProfileEnvName) && p != "" && p != f.DefaultProfile {
			// A PROFILE LEFT THERE: the agent's credential may come from it, and this launch
			// hands the agent no such profile, so the default profile's region may not be its.
			// Naming the default profile itself changes nothing, so it reads on.
			l.Profile, l.stranded, l.strandedProfile = p, f.ProfileEnvName, p
			l.Problem = f.ProfileEnvName + "=" + p + " is set in the environment yolo was launched " +
				"from, which this launch does not deliver, so which profile the agent's credential " +
				"comes from is not known"
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
	if l.File == "" && l.Home != "" {
		l.File = filepath.Join(l.Home, filepath.FromSlash(f.Path))
	}
	if l.stranded != "" {
		if l.File == "" {
			l.File = "~/" + f.Path
		}
		return l
	}
	if l.File == "" {
		l.File = "~/" + f.Path
		l.Problem = "not read: the environment yolo was launched from names no home directory"
		return l
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

// inheritedRegion is the region an agent whose credential pointer is inherited from the
// launching jail takes from the environment yolo was launched from: the first of regionVars set
// there to one DNS label, and the variable it was read from. "" when d's fold inherits no pointer
// or none of them holds a region.
func (s *CredentialScope) inheritedRegion(src *RegionFileSource, d *AgentDelivery, regionVars []string) (region, from string) {
	if s.served == nil || src.Getenv == nil {
		return "", ""
	}
	inherits := false
	for _, e := range d.Fold {
		if e.ServedBy != "" && s.served.Inherits(e.ServedBy) {
			inherits = true
			break
		}
	}
	if !inherits {
		return "", ""
	}
	for _, v := range regionVars {
		if r := strings.TrimSpace(src.Getenv(v)); r != "" && packdecl.RegionProblem("r", r) == "" {
			return r, v
		}
	}
	return "", ""
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
// inner whitespace runs collapsed so `[profile  dev]` is `[profile dev]`, and a quoted name
// unquoted, so `[profile "dev"]` is too; a line whose first non-blank character is `#` or `;` is
// a comment, and so is the rest of a line from a `#` or `;` that follows whitespace, as Claude
// Code's bundled AWS loader and codex's aws-config crate both read it; key names compare
// case-insensitively. A line indented past the key before it is that key's continuation or
// sub-setting (AWS's nested `s3 =` block), never a key of the section, which is how
// configparser, the reader botocore uses, reads it. Where the readers differ, this reads as the
// most lenient of them does, since a region it reads is delivered only after RegionProblem
// accepts it, and one it misses refuses a launch the agent could have made.
func iniValue(data []byte, section, key string) (value string, sectionFound, keyFound bool) {
	in := false
	keyIndent := -1
	for _, raw := range strings.Split(string(data), "\n") {
		raw = strings.TrimRight(raw, "\r")
		line := strings.TrimSpace(raw)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		line = stripInlineComment(line)
		if line[0] == '[' {
			in, keyIndent = false, -1
			if end := strings.IndexByte(line, ']'); end > 0 {
				if sectionName(line[1:end]) == section {
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

// stripInlineComment is line up to a `#` or `;` that follows whitespace, trimmed: an inline
// comment, as the AWS SDKs read one. A `#` with no whitespace before it is part of the value.
func stripInlineComment(line string) string {
	for i := 1; i < len(line); i++ {
		if (line[i] == '#' || line[i] == ';') && (line[i-1] == ' ' || line[i-1] == '\t') {
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}

// sectionName is a header's name as a section is compared: whitespace runs collapsed, and the
// last word unquoted when single or double quotes enclose it (`profile "dev"` is `profile dev`).
func sectionName(header string) string {
	fields := strings.Fields(header)
	if n := len(fields); n >= 2 {
		if last := fields[n-1]; len(last) >= 2 && (last[0] == '"' || last[0] == '\'') && last[len(last)-1] == last[0] {
			fields[n-1] = last[1 : len(last)-1]
		}
	}
	return strings.Join(fields, " ")
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
	if l.stranded != "" {
		return "    " + l.fileLabel() + " was not read: " + l.Problem
	}
	why := l.Problem
	if why == "" {
		// Delivered, and still no region reached the agent: something after the gate removed it
		// (an env_sources null at `yolo host`).
		why = "gave " + quoted(l.Region) + ", delivered as " + l.Var + ", which this launch then removed"
	}
	return "    " + l.fileLabel() + " (" + l.profileClause() + "): " + why
}

// remedy is the file's way to set a region, for the refusal's "set one" line: its key under the
// profile's section, or, when a profile left where yolo was launched kept it unread, delivering
// that profile so its section is read. "" when a region left there did, since the env_sources
// remedy beside it already says how to deliver one, and editing the file would change nothing.
func (l *RegionFileLookup) remedy() string {
	switch {
	case l.strandedProfile != "":
		return l.stranded + "=" + l.strandedProfile + " in an env_sources entry, so [" + l.Section +
			"]'s " + l.Key + " is read"
	case l.stranded != "":
		return ""
	}
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
		key := strings.Join([]string{l.Provider, l.Var, l.Region, l.File, l.Section, l.ProfileFrom, l.FromParent}, "\x00")
		if g, ok := groups[key]; ok {
			g.agents = append(g.agents, agent)
			continue
		}
		groups[key] = &group{provider: l.Provider, l: l, agents: []string{agent}}
		order = append(order, key)
	}
	sort.Strings(order)
	var lines []string
	for _, key := range order {
		g := groups[key]
		if g.l.FromParent != "" {
			lines = append(lines, "Region: "+g.l.Var+"="+g.l.Region+" for "+andList(g.agents)+
				" on provider "+quoted(g.provider)+", the launching jail's "+g.l.FromParent+
				", which came with the credential pointer this nested launch inherits from it: the "+
				"provider sets no region, and no region variable "+reachesWhom(g.agents))
			continue
		}
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
