package run

// launchcontext.go writes the launch log's ARCHAEOLOGY RECORDS: the facts about a launch that
// decide what it did and that no line it prints states, written to the log file alone.
//
// # Why these, and why the log alone
//
// Measured on 2026-10-11: four launches of one workspace whose config said
// `"profile": {"*": "bedrock", "claude": null}` ran claude on its subscription three times and
// on Bedrock the fourth, and the launch log could not say why. Its header named the version,
// the workspace and the environment dials; it did not record the argv (the fourth was started
// with `-p bedrock`), where each agent's profile came from, what in the effective config had
// changed since the previous launch, or that the boot re-applied a captured in-jail edit
// (`ultracode: true` in claude's settings overlay) on every one of them. So each record below
// answers one of those questions, after the fact, from the file.
//
// They go to the FILE and not the terminal by report-tiers.md's ruling for the launch stream
// (OQ-RO3: no quiet mode, so the answer to "too much" is reading the file, never another line on
// the terminal): each is either already said on the terminal in another form (the profile
// disclosure, the config-change prompt) or is provenance a person at the terminal did not ask
// for. teeLog.WriteLog is the channel: the log half of the launch stream, nothing on the
// terminal.
//
// # What they never contain
//
// A value that may be a credential. The argv's command is shown whole except where a NAME says
// its value may be a secret (an environment assignment `API_KEY=…`, a flag `--token …`), by the
// name-only rule internal/sensitivename states; the config diff replaces the value of every key
// whose name says so; and a captured edit is named by its key path, with only a boolean or a
// deletion shown, because those cannot carry a credential and are exactly the edits that change
// behavior (`ultracode=true`).
//
// # The state they compare against
//
// The previous launch's effective config and pack identities, persisted by THIS launch for the
// next one: `<workspace>/.yolo/last-launch-config.json` (the canonical snapshot `yolo config
// dump` prints) and `<workspace>/.yolo/last-launch.json` (when, the packs, the config files), or
// for `yolo host --` the same two under GLOBAL_STORAGE/host-launches, keyed by the directory's
// short code. Nothing reads either back to DECIDE anything — a jail can write the workspace's
// pair, and all it could do is make a later log's diff lie — which is what keeps them a record
// rather than a trust input, as launch.log itself is.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/sensitivename"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// LastLaunchConfigName and LastLaunchFactsName are the two files a launch leaves for the next
// launch's records, under <workspace>/.yolo/.
const (
	LastLaunchConfigName = "last-launch-config.json"
	LastLaunchFactsName  = "last-launch.json"
)

// redactedValue is what stands in a record for a value whose name says it may be a credential.
const redactedValue = "<redacted>"

// logRecord writes one archaeology record to this launch's log alone (LaunchLogOnly): never
// to the terminal, and nowhere at all when there is no log.
func (o *Options) logRecord(format string, args ...any) {
	fmt.Fprintf(LaunchLogOnly(o.Stderr), "  "+format+"\n", args...)
}

// formatInvocation is a launch's argv as one copy-pasteable line, `yolo <yolo's own tokens>
// <the command>`, the command redacted (RedactCommandArgs). argv is os.Args[1:] as typed; cmd is
// the command the parse found (Options.Args), which ends argv whether or not a `--` came before
// it. nil argv is a launch nothing typed: a capture or build jail yolo started itself.
func formatInvocation(argv, cmd []string) string {
	if argv == nil {
		return "(none typed: a launch yolo started itself)"
	}
	own, command := splitInvocation(argv, cmd)
	parts := []string{"yolo"}
	if len(own) > 0 {
		parts = append(parts, shquote.Join(own))
	}
	if len(command) > 0 {
		parts = append(parts, shquote.Join(RedactCommandArgs(command)))
	}
	return strings.Join(parts, " ")
}

// splitInvocation splits argv into yolo's own tokens and the command: the command is cmd when
// argv ends with it, else everything after the first `--`.
func splitInvocation(argv, cmd []string) (own, command []string) {
	if n := len(cmd); n > 0 && n <= len(argv) {
		tail := argv[len(argv)-n:]
		same := true
		for i := range tail {
			if tail[i] != cmd[i] {
				same = false
				break
			}
		}
		if same {
			return argv[:len(argv)-n], cmd
		}
	}
	for i, a := range argv {
		if a == "--" {
			return argv[:i+1], argv[i+1:]
		}
	}
	return argv, nil
}

// envAssignment matches a shell environment assignment, `NAME=value`.
var envAssignment = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// RedactCommandArgs is a command's argv with every value whose NAME says it may be a credential
// replaced: `API_KEY=sk-…` becomes `API_KEY=<redacted>`, `--token=…` becomes `--token=<redacted>`,
// and the token after a bare `--token` or `-api-key` becomes `<redacted>`. Everything else is
// kept as typed, because the command is the launch's single most useful fact and boot.log's
// `⚡ Executing:` line already holds it whole in the same directory.
func RedactCommandArgs(args []string) []string {
	out := make([]string, len(args))
	redactNext := false
	for i, a := range args {
		if redactNext {
			out[i] = redactedValue
			redactNext = false
			continue
		}
		out[i] = a
		if m := envAssignment.FindStringSubmatch(a); m != nil {
			if sensitivename.Is(m[1]) {
				out[i] = m[1] + "=" + redactedValue
			}
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 && a != "--" {
			name, _, glued := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if !sensitivename.Is(name) {
				continue
			}
			if glued {
				out[i] = a[:strings.Index(a, "=")+1] + redactedValue
			} else {
				redactNext = true
			}
		}
	}
	return out
}

// ProfileRecord is the profile record's body: every CLI the selected packs install, then every
// other CLI the fold names, each with its active set and where that came from, read off the
// fold's own Origins. key is the config `profile` key's selection (the fold's selection 0); flag
// spells an entry the -p flags made (selection 1), given the CLI and whether it was a named one.
func ProfileRecord(fold config.ProfileFold, bins []string, key config.ProfileSelection,
	flag func(cli string, named bool) string) string {
	seen := map[string]bool{}
	var order []string
	for _, b := range bins {
		if !seen[b] {
			seen[b] = true
			order = append(order, b)
		}
	}
	if fold.Table != nil {
		for _, k := range fold.Table.Keys() {
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	if len(order) == 0 {
		return "no selected pack installs a CLI, so nothing selects a profile"
	}
	parts := make([]string, 0, len(order))
	for _, cli := range order {
		v, inTable := any(nil), false
		if fold.Table != nil {
			v, inTable = fold.Table.Get(cli)
		}
		if !inTable {
			parts = append(parts, cli+"=none (nothing selects one)")
			continue
		}
		origin := fold.Origins[cli]
		var source string
		switch {
		case origin.From == 0 && key.Key == config.ActiveProviderKey && origin.Named:
			source = "config " + config.ActiveProviderKey + "." + cli
		case origin.From == 0 && key.Key == config.ActiveProviderKey:
			source = "config " + config.ActiveProviderKey + "." + config.ActiveProviderEveryAgent
		case origin.From == 0 && origin.Named:
			source = "config profile." + cli
		case origin.From == 0 && len(key.Named) > 0:
			source = `config profile."*"`
		case origin.From == 0:
			source = "config profile"
		default:
			source = flag(cli, origin.Named)
		}
		parts = append(parts, cli+"="+profileValueText(v)+" ("+source+")")
	}
	return strings.Join(parts, ", ")
}

// profileValueText is one fold Table value as the record writes it: a set of one as its name, a
// longer one comma-joined, no selection as `none`.
func profileValueText(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []any:
		names := make([]string, 0, len(val))
		for _, e := range val {
			names = append(names, fmt.Sprint(e))
		}
		return strings.Join(names, ",")
	}
	return "none"
}

// jailProfileFlag spells an entry a jail launch's -p flags made.
func (o *Options) jailProfileFlag(cli string, named bool) string {
	sel := o.ProfileFlags()
	if named {
		return "-p " + cli + "=" + strings.Join(sel.Named[cli], ",")
	}
	return "-p " + strings.Join(sel.Default, ",")
}

// launchFacts is what one launch leaves for the next one's comparison (LastLaunchFactsName).
type launchFacts struct {
	// Launched is when the launch that wrote the facts recorded them.
	Launched string `json:"launched"`
	// Packs is each selected pack's identity, by name: its origin and content digest.
	Packs map[string]string `json:"packs"`
	// Files is each config file's identity, by path: its modification time and digest, or
	// "absent".
	Files map[string]string `json:"files"`
}

// launchStateStore reads and writes the previous launch's two files.
type launchStateStore interface {
	read(name string) ([]byte, error)
	write(name string, data []byte) error
}

// workspaceStateStore is a jail launch's: under <workspace>/.yolo, beneath a root on it.
type workspaceStateStore struct{ workspace string }

func (s workspaceStateStore) read(name string) ([]byte, error) {
	f, err := paths.OpenWorkspaceStateFile(s.workspace, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (s workspaceStateStore) write(name string, data []byte) error {
	f, err := paths.OpenWorkspaceStateFile(s.workspace, name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// dirStateStore is a `yolo host --` launch's: <dir>/<prefix><name>, 0600 in a 0700 directory,
// since a host launch has no workspace (HostLaunchLog says why not the cwd).
type dirStateStore struct{ dir, prefix string }

func (s dirStateStore) read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.dir, s.prefix+name))
}

func (s dirStateStore) write(name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, s.prefix+name), data, 0o600)
}

// HostLaunchStateDir is where `yolo host --` keeps the previous launch's files, one pair per
// directory short code.
func HostLaunchStateDir() string { return filepath.Join(paths.GlobalStorage(), "host-launches") }

// configChangeRecords compares this launch's effective config (its canonical snapshot), pack
// set and config files with the previous launch's in store, returns the records saying what
// changed, and stores this launch's for the next. now stamps the stored facts.
func configChangeRecords(store launchStateStore, snapshot string, packs []*packload.Pack,
	files []string, now time.Time) []string {
	// A private env value never reaches the log or the stored snapshot, which the jail can read
	// beside the log; the previous snapshot is redacted too, for one an older launch stored.
	snapshot = config.RedactPrivateEnvSnapshot(snapshot)
	cur := launchFacts{
		Launched: now.Format("2006-01-02T15:04:05-0700"),
		Packs:    map[string]string{},
		Files:    map[string]string{},
	}
	for _, p := range packs {
		cur.Packs[p.Name] = packIdentity(p)
	}
	for _, f := range files {
		cur.Files[f] = fileIdentity(f)
	}

	var prev *launchFacts
	if data, err := store.read(LastLaunchFactsName); err == nil {
		var f launchFacts
		if json.Unmarshal(data, &f) == nil {
			prev = &f
		}
	}
	prevSnapshot, snapErr := store.read(LastLaunchConfigName)
	prevSnapshot = []byte(config.RedactPrivateEnvSnapshot(string(prevSnapshot)))

	var out []string
	out = append(out, "config files: "+fileRecord(cur.Files, prev))
	switch {
	case prev == nil || snapErr != nil:
		out = append(out, "config: no previous launch recorded")
	default:
		diff := config.TextDiff(string(prevSnapshot), snapshot,
			"previous launch ("+prev.Launched+")", "this launch")
		if len(diff) == 0 {
			out = append(out, "config: effective config unchanged since the previous launch ("+prev.Launched+")")
		} else {
			out = append(out, "config: effective config changed since the previous launch ("+prev.Launched+"):")
			for _, l := range compactDiff(diff) {
				out = append(out, "  "+l)
			}
		}
	}
	out = append(out, "packs: "+packRecord(cur.Packs, prev))

	if data, err := json.MarshalIndent(cur, "", "  "); err == nil {
		_ = store.write(LastLaunchConfigName, []byte(snapshot+"\n"))
		_ = store.write(LastLaunchFactsName, append(data, '\n'))
	}
	return out
}

// maxDiffLines bounds one config diff in the log; a longer one ends with how much it left out.
const maxDiffLines = 200

// compactDiff is diff with each sensitive key's value redacted and the length bounded.
func compactDiff(diff []string) []string {
	out := make([]string, 0, len(diff))
	for i, l := range diff {
		if i == maxDiffLines {
			out = append(out, fmt.Sprintf("… %d more diff lines (diff the two snapshots for the rest)", len(diff)-i))
			break
		}
		out = append(out, redactDiffLine(l))
	}
	return out
}

// diffKeyLine matches a snapshot line holding one key and a scalar value, after the diff's
// one-character marker.
var diffKeyLine = regexp.MustCompile(`^([ +\-])(\s*)"((?:[^"\\]|\\.)*)": (.*?)(,?)$`)

// redactDiffLine replaces the value on a snapshot diff line whose key's name says it may be a
// credential (sensitivename). An object or list opening on the line is left, its entries being
// lines of their own.
func redactDiffLine(l string) string {
	m := diffKeyLine.FindStringSubmatch(l)
	if m == nil || strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") {
		return l
	}
	if !sensitivename.Is(m[3]) || m[4] == "{" || m[4] == "[" || m[4] == "{}" || m[4] == "[]" {
		return l
	}
	return m[1] + m[2] + `"` + m[3] + `": "` + redactedValue + `"` + m[5]
}

// packIdentity is a pack's record: its origin and the first 12 hex digits of its content digest.
func packIdentity(p *packload.Pack) string {
	origin := "fetched"
	switch {
	case p.Official:
		origin = "embedded"
	case p.Local:
		origin = "local"
	}
	d, err := packContentDigest(p.Root)
	if err != nil {
		return origin + " (content unreadable)"
	}
	return origin + " " + shortHex(d)
}

// fileIdentity is a config file's record: its modification time and digest, or "absent".
func fileIdentity(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "absent"
		}
		return "unreadable"
	}
	sum := sha256.Sum256(data)
	mod := ""
	if st, err := os.Stat(path); err == nil {
		mod = "modified " + st.ModTime().Format("2006-01-02T15:04:05-0700") + ", "
	}
	return mod + "sha256 " + shortHex(hex.EncodeToString(sum[:]))
}

func shortHex(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// fileRecord names each config file with its identity, marking one that changed since prev.
func fileRecord(files map[string]string, prev *launchFacts) string {
	names := sortedFactKeys(files)
	if len(names) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(names))
	for _, f := range names {
		s := f + " (" + files[f] + ")"
		if prev != nil {
			if was, ok := prev.Files[f]; ok && was != files[f] {
				s += " CHANGED, was (" + was + ")"
			}
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// packRecord is the pack set, against prev's when there is one: added (+), removed (-) and
// changed (~) packs by name, or the set unchanged.
func packRecord(packs map[string]string, prev *launchFacts) string {
	names := sortedFactKeys(packs)
	if prev == nil {
		if len(names) == 0 {
			return "none selected (no previous launch recorded)"
		}
		parts := make([]string, 0, len(names))
		for _, n := range names {
			parts = append(parts, n+" ("+packs[n]+")")
		}
		return strings.Join(parts, ", ") + " (no previous launch recorded)"
	}
	var changes []string
	for _, n := range names {
		was, ok := prev.Packs[n]
		switch {
		case !ok:
			changes = append(changes, "+ "+n+" ("+packs[n]+")")
		case was != packs[n]:
			changes = append(changes, "~ "+n+" ("+was+" → "+packs[n]+")")
		}
	}
	for _, n := range sortedFactKeys(prev.Packs) {
		if _, ok := packs[n]; !ok {
			changes = append(changes, "- "+n)
		}
	}
	if len(changes) == 0 {
		if len(names) == 0 {
			return "unchanged since the previous launch (none selected)"
		}
		return "unchanged since the previous launch (" + strings.Join(names, ", ") + ")"
	}
	return "changed since the previous launch: " + strings.Join(changes, ", ")
}

func sortedFactKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// launchSurfaces is every surface a launch over packs composes: core's own and each pack's, as
// the boot's surface loop reads them.
func launchSurfaces(packs []*packload.Pack) []manifest.Surface {
	var extra []manifest.Surface
	for _, p := range packs {
		if s, probs := p.Surfaces(); len(probs) == 0 {
			extra = append(extra, s...)
		}
	}
	m, err := agentcfg.ManifestWith(extra...)
	if err != nil {
		m = agentcfg.BuiltinManifest()
		return append(m.Surfaces(), extra...)
	}
	return m.Surfaces()
}

// capturedEditRecords is one record per surface whose capture store in store (a root on the
// sidecar directory, nil for none) holds a non-empty overlay or list capture: the surface, its
// captured key paths, and how to discard them. reapplied says when the render re-applies them;
// reset spells the discard for an agent/surface. One "none" record when there are none.
func capturedEditRecords(store *os.Root, surfaces []manifest.Surface, reapplied string,
	reset func(slug string) string) []string {
	if store == nil {
		return []string{"captured edits: none (no capture store)"}
	}
	seen := map[string]bool{}
	type entry struct{ agent, name, path string }
	var entries []entry
	for _, s := range surfaces {
		k := s.Agent + "-" + s.Name
		if !seen[k] {
			seen[k] = true
			entries = append(entries, entry{s.Agent, s.Name, s.Path})
		}
	}
	// The `user` host_files surfaces, whose names live in config rather than the manifest: found
	// from their sidecars, as `yolo config ls` finds them.
	if des, err := fs.ReadDir(store.FS(), "."); err == nil {
		for _, de := range des {
			n := de.Name()
			if !strings.HasPrefix(n, "user-") || !strings.HasSuffix(n, ".overlay.json") {
				continue
			}
			slug := strings.TrimSuffix(strings.TrimPrefix(n, "user-"), ".overlay.json")
			if k := "user-" + slug; !seen[k] {
				seen[k] = true
				entries = append(entries, entry{"user", slug, ""})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].agent != entries[j].agent {
			return entries[i].agent < entries[j].agent
		}
		return entries[i].name < entries[j].name
	})
	var out []string
	for _, e := range entries {
		base := e.agent + "-" + e.name
		var keys []string
		if data, err := paths.ReadRegularFileBeneath(store, base+".overlay.json"); err == nil {
			if v, err := jsonx.Decode(data); err == nil {
				if m, ok := v.(*jsonx.OrderedMap); ok {
					keys = overlayKeyRecords("", m)
				}
			}
		}
		if data, err := paths.ReadRegularFileBeneath(store, base+render.ListCaptureSuffix); err == nil {
			lpaths, recs := agentcfg.ListCaptureRecords(data)
			for _, p := range lpaths {
				r := recs[p]
				if len(r.Add)+len(r.Remove) == 0 {
					continue
				}
				keys = append(keys, fmt.Sprintf("%s (list: +%d -%d entries)", p, len(r.Add), len(r.Remove)))
			}
		}
		if len(keys) == 0 {
			continue
		}
		where := e.agent + "/" + e.name
		if e.path != "" {
			where += " (" + e.path + ")"
		}
		out = append(out, fmt.Sprintf("captured edits: %s, %s: %s; `%s` discards them",
			where, reapplied, strings.Join(keys, ", "), reset(e.agent+"/"+e.name)))
	}
	if len(out) == 0 {
		return []string{"captured edits: none (no surface of this launch holds a captured edit)"}
	}
	return out
}

// overlayKeyRecords names every leaf of a capture overlay by its dotted key path. A boolean is
// shown with its value and a deletion (an overlay's null) as `(removed)`: neither can carry a
// credential, and those are the edits that switch behavior. Every other value is named by its
// kind alone, because a string or a number may be a key or a token.
func overlayKeyRecords(prefix string, m *jsonx.OrderedMap) []string {
	var out []string
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		switch val := v.(type) {
		case *jsonx.OrderedMap:
			if len(val.Keys()) == 0 {
				out = append(out, path+" (object)")
				continue
			}
			out = append(out, overlayKeyRecords(path, val)...)
		case bool:
			out = append(out, fmt.Sprintf("%s=%t", path, val))
		case nil:
			out = append(out, path+" (removed)")
		case string:
			out = append(out, path+" (string)")
		case []any:
			out = append(out, path+" (list)")
		default:
			out = append(out, path+" (number)")
		}
	}
	return out
}

// recordLaunchContext writes a jail launch's context records — the jail, the profile selection
// and its sources, the config and pack changes since the previous launch, and the captured edits
// the boot re-applies — to the launch log alone. Called once the launch's channel has composed,
// so the profile record reads the very fold the channel did. Under the seal (a fork's build) the
// config, the profile and the captures do not cross, so only the jail is recorded.
func (o *Options) recordLaunchContext(cfg *jsonx.OrderedMap, packs []*packload.Pack, cname, rt string) {
	if _, ok := LaunchLogOnly(o.Stderr).(logOnly); !ok {
		return // no log: nothing to write, and nothing to persist for a later record
	}
	o.logRecord("jail: %s  runtime=%s  notch=%s", cname, rt, o.atNotch)
	// A container launch records its attach-or-fresh decision where it makes it (run.go's
	// arrival loop and attachExisting); a macos-user one has made its own by now
	// (arriveMacosUser), and has no container to attach to.
	if k := o.macosUserKey; k != nil {
		switch {
		case k.joined != nil:
			o.logRecord("decision: macos-user session joining this workspace's running keeper")
		case k.keeperless:
			o.logRecord("decision: fresh macos-user session (nothing a keeper holds)")
		default:
			o.logRecord("decision: fresh macos-user session (its own keeper)")
		}
	}
	if o.Sealed {
		o.logRecord("sealed build launch: no config, profile or captured edit crosses")
		return
	}
	o.logRecord("profile: %s", ProfileRecord(o.profileFold(cfg, packs), config.ReceiversOf(packs).Bins,
		config.ConfigActiveSelection(cfg), o.jailProfileFlag))
	if snap, err := config.SnapshotJSON(cfg); err == nil {
		files := []string{paths.UserConfigPath(), filepath.Join(o.Workspace, config.WorkspaceConfigName)}
		for _, line := range configChangeRecords(workspaceStateStore{o.Workspace}, snap, packs, files, time.Now()) {
			o.logRecord("%s", line)
		}
	}
	prism, err := paths.OpenWorkspacePrismRoot(o.Workspace, o.homeRel())
	if err != nil {
		prism = nil
	} else {
		defer prism.Close()
	}
	for _, line := range capturedEditRecords(prism, launchSurfaces(packs), "re-applied on every boot",
		func(slug string) string { return "yolo config reset " + slug }) {
		o.logRecord("%s", line)
	}
}
