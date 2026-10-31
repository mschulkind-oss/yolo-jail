package packsrc

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// Object is the ordered-map shape used at the config boundary. packsrc accepts this
// small interface rather than depending on a config representation or decoding files.
type Object interface {
	Keys() []string
	Get(string) (any, bool)
}

// SourceDefinition describes one named tree of packs.
type SourceDefinition struct {
	Name string
	Path string // local tree root, or empty for a git tree
	Repo string // normalized git repository URL, or empty for a local tree
	Ref  string // authored git ref, or empty to pin the repository's advertised default
	// Bare delivers every pack of this source under its own names, with no namespace
	// (docs/design/pack-naming.md PN-D7): `{ path = "…", bare = true }`.
	Bare bool
	// Marked says the source's table carries a `bare` key, true or false: its user chose. A source
	// without one gets the default namespace (its key), which main's migrator never wrote.
	Marked bool
	// Shorthand says the source is the plain-path string form, `key = "~/path"`, which has no
	// field to set: the step that marks it bare rewrites it as a table.
	Shorthand bool
}

// KeepNamesStep is the one command that delivers every pack of this source under its own names
// (`bare = true`), for a message whose next step that is.
func (d SourceDefinition) KeepNamesStep() string { return d.bareStep(true) }

// AcceptNamesStep is the one command that keeps this source's default namespace and says so
// (`bare = false`), which silences the converted-config warning.
func (d SourceDefinition) AcceptNamesStep() string { return d.bareStep(false) }

func (d SourceDefinition) bareStep(bare bool) string {
	key := "pack_sources." + ConfigKey(d.Name)
	if d.Shorthand {
		return ConfigCommand("set", key, fmt.Sprintf("{ path = %s, bare = %t }", strconv.Quote(d.Path), bare))
	}
	return ConfigCommand("set", key+".bare", strconv.FormatBool(bare))
}

// ConfigKey spells one key of a `yolo config set` path: as written when it is a plain word of
// letters, digits, `_` and `-`, and quoted otherwise, so a key such as `skills.git` (which main's
// migrator made from a repository URL) stays one component rather than two.
func ConfigKey(k string) string {
	plain := k != ""
	for _, r := range k {
		plain = plain && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}
	if plain {
		return k
	}
	return strconv.Quote(k)
}

// ConfigCommand is `yolo config <verb> <path> <value> --user`, shell-quoted and in backticks, for
// a message to name as its next step.
func ConfigCommand(verb, path, value string) string {
	return "`" + shquote.Join([]string{"yolo", "config", verb, path, value, "--user"}) + "`"
}

// entryBareStep is the one command that sets `bare` on the `packs` entry origin names: the
// entry's own key for a table, a table replacing a plain-string handle, or, for a handle not yet
// in the config ("pack handle", `yolo pack add`'s check), the table to add instead.
func entryBareStep(origin, handle string, table, bare bool) string {
	entry := fmt.Sprintf("{ pack = %s, bare = %t }", strconv.Quote(handle), bare)
	rest, ok := strings.CutPrefix(origin, "config.packs[")
	if ok {
		rest, ok = strings.CutSuffix(rest, "]")
	}
	index, err := strconv.Atoi(rest)
	if !ok || err != nil || index < 0 {
		return ConfigCommand("add", "packs", entry)
	}
	path := fmt.Sprintf("packs[%d]", index)
	if table {
		return ConfigCommand("set", path+".bare", strconv.FormatBool(bare))
	}
	return ConfigCommand("set", path, entry)
}

// PackReference is one pack named through the readable source vocabulary. Source is
// the physical source address; Name is the author-facing handle and never comes from
// a manifest. LocalPath is set only for local references.
type PackReference struct {
	Source          string
	Name            string
	SourceID        string
	LocalPath       string
	EmbeddedName    string
	AllowDefaultRef bool
	Origin          string
	// Namespace is the word every agent-facing name of the pack starts with
	// (docs/design/pack-naming.md §3.3), "" for a pack delivered bare: a shipped pack, or an
	// entry or source marked `bare = true`.
	Namespace string
	// NamespaceOwner says whose word Namespace is — "source:<key>", "local:<pack>" or
	// "repo:<repository>" — so two entries of one source share it and any other two that
	// chose one word are a clash (§3.5). "" when Namespace is "".
	NamespaceOwner string
	// Landing is the directory a `files` tree and a `/ctx/host-…` grant land under (§3.4):
	// `<namespace>-<pack>`, `<namespace>` for a pack that is its own namespace. "" for a pack
	// delivered bare, shipped or marked, which lands exactly where it did before pack naming
	// (PN-D7: bare means unchanged).
	Landing string
	// KeepNamesStep is set when Namespace is the DEFAULT one, chosen by neither the entry nor its
	// source (no `bare`, no `namespace`) — the shape main's migrator wrote — and is the one step
	// that keeps the pack's skills under their own names.
	KeepNamesStep string
	// AcceptNamesStep, set with KeepNamesStep, is the one step that keeps the default namespace
	// and records the choice (`bare = false`).
	AcceptNamesStep string
	// BareStep is set whenever Namespace is: the one command that lists this entry bare, the
	// remedy for two of its names that its namespace delivers alike (§3.5).
	BareStep string
}

// ParseSourceDefinitions validates the raw pack_sources table without loading any
// config file. Local paths are retained verbatim for the caller to expand against its
// home directory; git URLs are normalized into the packsrc transport spelling.
func ParseSourceDefinitions(raw any) (map[string]SourceDefinition, error) {
	if raw == nil {
		return map[string]SourceDefinition{}, nil
	}
	m, ok := raw.(Object)
	if !ok {
		return nil, fmt.Errorf("pack_sources: expected a table")
	}
	out := make(map[string]SourceDefinition, len(m.Keys()))
	for _, name := range m.Keys() {
		if name == "local" || name == "yolo" {
			return nil, fmt.Errorf("pack_sources.%s: %q is yolo's own word for a pack source; choose another key", name, name)
		}
		if !validHandleSegment(name) {
			return nil, fmt.Errorf("pack_sources.%s: invalid source name", name)
		}
		// THE KEY IS THE NAMESPACE (PN-D11), but a key that is not a valid namespace word still
		// loads — main's migrator wrote folder names as keys — and is refused only where a
		// namespace would be composed from it (named), with the line that marks it bare.
		value, _ := m.Get(name)
		def := SourceDefinition{Name: name}
		const shape = "expected a local path, { path = PATH }, or { git = URL, ref = REF }, each table taking bare = true"
		switch v := value.(type) {
		case string:
			if !strings.HasPrefix(v, "~/") && !filepath.IsAbs(v) {
				return nil, fmt.Errorf("pack_sources.%s: local source must be an absolute or ~/ path", name)
			}
			def.Path, def.Shorthand = v, true
		default:
			obj, ok := value.(Object)
			if !ok || len(obj.Keys()) == 0 {
				return nil, fmt.Errorf("pack_sources.%s: %s", name, shape)
			}
			for _, key := range obj.Keys() {
				if key != "git" && key != "ref" && key != "path" && key != "bare" {
					return nil, fmt.Errorf("pack_sources.%s.%s: unknown source field (path, git, ref or bare)", name, key)
				}
			}
			bare, err := explicitBare(obj, "pack_sources."+name)
			if err != nil {
				return nil, err
			}
			def.Bare = bare
			_, def.Marked = obj.Get("bare")
			rawPath, hasPath := obj.Get("path")
			rawGit, hasGit := obj.Get("git")
			if hasPath && hasGit {
				return nil, fmt.Errorf("pack_sources.%s: a source is a path or a repository, not both", name)
			}
			if hasPath {
				path, isString := rawPath.(string)
				if !isString || (!strings.HasPrefix(path, "~/") && !filepath.IsAbs(path)) {
					return nil, fmt.Errorf("pack_sources.%s.path: local source must be an absolute or ~/ path", name)
				}
				if _, hasRef := obj.Get("ref"); hasRef {
					return nil, fmt.Errorf("pack_sources.%s.ref: a local source takes no ref", name)
				}
				def.Path = path
				break
			}
			repoURL, isString := rawGit.(string)
			if !hasGit || !isString {
				return nil, fmt.Errorf("pack_sources.%s: %s", name, shape)
			}
			ref, err := explicitRef(obj, "pack_sources."+name)
			if err != nil {
				return nil, err
			}
			repo, err := normalizeRepository(repoURL)
			if err != nil {
				return nil, fmt.Errorf("pack_sources.%s.git: %w", name, err)
			}
			def.Repo, def.Ref = repo, ref
		}
		out[name] = def
	}
	return out, nil
}

// LocalDirectoryIdentity resolves a local pack directory through symlinks when it
// exists, so two authored handles for the same physical tree compare equal.
func LocalDirectoryIdentity(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	physical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(physical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}
	return filepath.Clean(physical), nil
}

func validHandleSegment(s string) bool {
	if s == "" || s == "." || s == ".." || strings.TrimSpace(s) != s || strings.ContainsAny(s, "\\:/?#") {
		return false
	}
	return true
}

func explicitRef(obj Object, origin string) (string, error) {
	rawRef, hasRef := obj.Get("ref")
	if !hasRef {
		return "", nil
	}
	ref, valid := rawRef.(string)
	if !valid || strings.TrimSpace(ref) != ref || ref == "" {
		return "", fmt.Errorf("%s.ref: expected a non-empty string", origin)
	}
	return ref, nil
}

func normalizeRepository(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s != raw {
		return "", fmt.Errorf("expected an unqualified git repository URL")
	}
	s = strings.TrimPrefix(s, "git+")
	u, err := url.Parse(s)
	if err != nil || u.Path == "" ||
		(u.Scheme != "https" && u.Scheme != "ssh" && u.Scheme != "http" && u.Scheme != "file") ||
		(u.Scheme == "file" && (u.Host != "" || !filepath.IsAbs(u.Path))) ||
		(u.Scheme != "file" && u.Host == "") ||
		hasPasswordUserinfo(u.User) {
		return "", fmt.Errorf("expected an ssh, https, http, or absolute file git repository URL without embedded credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("source repository URL takes no query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Scheme != "file" {
		u.Path = strings.TrimSuffix(u.Path, ".git")
	}
	if u.Path == "" || u.Path == "/" {
		return "", fmt.Errorf("repository URL must name a repository")
	}
	return u.String(), nil
}

func hasPasswordUserinfo(user *url.Userinfo) bool {
	if user == nil {
		return false
	}
	_, password := user.Password()
	return password
}

func ownerRepositoryName(repo string) (string, error) {
	u, err := url.Parse(repo)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("direct git packs must use an owner/repository URL")
	}
	owner, repository := parts[0], strings.TrimSuffix(parts[1], ".git")
	if !validHandleSegment(owner) || !validHandleSegment(repository) {
		return "", fmt.Errorf("direct git packs must use an owner/repository URL")
	}
	return owner + "/" + repository, nil
}

func gitPackAddress(repo, subdir, ref string) string {
	var b strings.Builder
	b.WriteString("git+")
	b.WriteString(repo)
	if subdir != "" {
		b.WriteString("//")
		b.WriteString(subdir)
	}
	if ref != "" {
		b.WriteString("?ref=")
		b.WriteString(url.QueryEscape(ref))
	}
	return b.String()
}

func expandTilde(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}
