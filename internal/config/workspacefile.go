package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// workspacefile.go is the PER-WORKSPACE FILE, a term coined here: a file yolo keeps for one
// workspace in ~/.config/yolo-jail/workspaces/ (paths.WorkspaceFilesDir), holding switches that
// apply to that workspace alone (docs/design/boundary-broker.md OQ-BB12, BB-D53 to BB-D57).
//
// # Why a file beside the user config, and not the user config
//
// The maintainer's ruling (2026-10-01, OQ-BB12): "I don't want to do anything that edits the user
// config directly. We can edit a file that lives next to it of a different name or whatever."
// config.jsonc is a hand-commented file nothing in yolo writes, and a read-modify-write through
// the JSONC decoder drops every comment in it. This file is yolo's own: `yolo loopholes enable`
// and `disable` write it, whole, and a hand edit is allowed but its comments are not kept.
//
// ⚠ IT IS NOT THE WITHDRAWN `config.local.jsonc` (userlayer.go's header), and the difference is
// the point. That file was a second copy of the WHOLE user config, merged for every workspace
// because it existed. This one is named by the workspace it governs, carries one key family
// today (`loopholes.<name>.enabled`, everything else refused), is written by a command whose
// last line names it, and is named by `yolo check` and located by every refusal of a key in it.
//
// # Its shape
//
//	{
//	  "workspace": "/home/you/code/app",
//	  "loopholes": { "github-broker": { "enabled": true } }
//	}
//
// The file is named `<the folder's name, sanitized>-<first 12 hex digits of the SHA-256 of the
// resolved workspace path>.jsonc`, so finding it costs one stat. THE `workspace` FIELD IS
// AUTHORITATIVE: a file whose field does not resolve to the workspace it was found for is
// ignored, and `yolo check` and the launch warn, naming both. Matching resolves symlinks with the
// widening entry's resolver (brokeredKeyNames, BB-D33), exact folder only.
//
// # Where it sits in the merge
//
// A user-scope layer for one workspace, merged LAST (BB-D54): over the user config and over the
// workspace's own yolo-jail.jsonc and yolo-jail.local.jsonc. Every key it may carry is a switch,
// and the human who ran the command for this workspace outranks a file the workspace's agent can
// edit. Nothing in it reaches a jail as a file: the delivery copy a jail reads
// (config-assembled.json) holds the merged VALUES, and the user scope a jail inherits is composed
// without this layer (LoadConfigWithoutWorkspaceFile), because its key is a host path no jail has
// — the reason `brokered` is not inherited either.

const (
	// workspaceFileWorkspaceKey is the field naming the workspace the file is for.
	workspaceFileWorkspaceKey = "workspace"
	workspaceFileLoopholesKey = "loopholes"
	workspaceFileEnabledKey   = "enabled"
)

var (
	knownWorkspaceFileKeys         = set(workspaceFileWorkspaceKey, workspaceFileLoopholesKey)
	knownWorkspaceFileLoopholeKeys = set(workspaceFileEnabledKey)
)

// workspaceFileHeader is the comment every write puts at the top of the file.
const workspaceFileHeader = `// yolo's per-workspace file for the workspace named below. ` + "`yolo loopholes enable`" + ` and
// ` + "`yolo loopholes disable`" + ` write it; it applies to that workspace alone, from its next fresh
// launch, and no jail can read it. You may edit it by hand, but yolo rewrites the whole file
// the next time one of those commands runs, so a comment you add is not kept.
`

// WorkspaceFilePath is workspace's per-workspace file. The workspace is resolved first
// (resolveWorkspaceForFile), so a link to the workspace and the path behind it, or two
// spellings of one folder on a case-insensitive file system, name one file.
func WorkspaceFilePath(workspace string) string {
	resolved := resolveWorkspaceForFile(workspace)
	sum := sha256.Sum256([]byte(resolved))
	return filepath.Join(paths.WorkspaceFilesDir(),
		workspaceFileSlug(filepath.Base(resolved))+"-"+hex.EncodeToString(sum[:])[:12]+".jsonc")
}

// resolveWorkspaceForFile is the workspace as the per-workspace file names it: `~/` expanded,
// made absolute, symlinks resolved, and each name spelled as its folder spells it
// (canonicalCase).
func resolveWorkspaceForFile(workspace string) string {
	return canonicalCase(expandAndResolve(workspaceOrCwd(workspace)))
}

// canonicalCase is p, an absolute path with its symlinks resolved, with each name spelled as
// the folder holding it lists it. On a case-insensitive file system (macOS's default) one folder
// answers to `~/Code/App` and `~/code/app`, and symlink resolution keeps each name as it was
// typed, so without this the two spellings would hash to two files, and a switch made under one
// would not reach a launch made under the other.
//
// A name is looked up in its folder's listing only when its case-flipped spelling is the same
// file, which is what a case-insensitive folder answers; on a case-sensitive file system that
// lookup fails, so the cost there is one Lstat per name holding a letter. A name that cannot be
// read is left as written, as is everything below it.
func canonicalCase(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	out := string(filepath.Separator)
	names := strings.Split(p, string(filepath.Separator))
	for i, name := range names {
		if name == "" {
			continue
		}
		next := filepath.Join(out, name)
		flipped := flipCase(name)
		if flipped == name {
			out = next
			continue
		}
		fi, err := os.Lstat(next)
		if err != nil {
			return filepath.Join(append([]string{out}, names[i:]...)...)
		}
		if alt, err := os.Lstat(filepath.Join(out, flipped)); err == nil && os.SameFile(fi, alt) {
			next = folderSpelling(out, name, fi)
		}
		out = next
	}
	return out
}

// folderSpelling is dir/name, with name as dir's listing spells the entry that is fi.
func folderSpelling(dir, name string, fi os.FileInfo) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return filepath.Join(dir, name)
	}
	for _, e := range entries {
		if e.Name() == name {
			return filepath.Join(dir, name)
		}
	}
	for _, e := range entries {
		if !strings.EqualFold(e.Name(), name) {
			continue
		}
		if efi, err := os.Lstat(filepath.Join(dir, e.Name())); err == nil && os.SameFile(fi, efi) {
			return filepath.Join(dir, e.Name())
		}
	}
	return filepath.Join(dir, name)
}

// flipCase is s with each letter's case swapped.
func flipCase(s string) string {
	return strings.Map(func(r rune) rune {
		if u := unicode.ToUpper(r); u != r {
			return u
		}
		return unicode.ToLower(r)
	}, s)
}

// workspaceFileSlug is the folder name as a file name can spell it: letters, digits, `.`, `_`
// and `-`, every run of anything else one `-`, no leading `.` or `-`, at most 40 characters, and
// "workspace" when nothing is left. It is for a reader listing the folder; the hash is the key.
func workspaceFileSlug(base string) string {
	var b strings.Builder
	dash := false
	for _, r := range base {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '.' || r == '_' || r == '-'
		if !ok {
			if !dash {
				b.WriteByte('-')
			}
			dash = true
			continue
		}
		b.WriteRune(r)
		dash = false
	}
	s := strings.Trim(b.String(), ".-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], ".-")
	}
	if s == "" {
		return "workspace"
	}
	return s
}

// WorkspaceFile is one workspace's per-workspace file as read.
type WorkspaceFile struct {
	// Path is the file.
	Path string
	// Workspace is the resolved workspace it was read for.
	Workspace string
	// Named is its `workspace` field as written, "" when absent or not a string.
	Named string
	// Applies is whether Named resolves to Workspace: only then is anything in it used.
	Applies bool
	// Loopholes is every well-formed switch, loophole name → enabled, in file order. A
	// malformed entry is left out and reported in Problems.
	Loopholes *jsonx.OrderedMap
	// Problems are its refusals, each a full message naming where in the file it is and the
	// fix. A launch refuses on any of them, and `yolo check` fails on them.
	Problems []string
	// unreadable is set when the file could not be read or parsed at all.
	unreadable bool
	// node is the file's provenance (sources.go), nil unless read with record.
	node *srcNode
}

// ReadWorkspaceFile reads workspace's per-workspace file; nil when there is none.
func ReadWorkspaceFile(workspace string) *WorkspaceFile {
	return readWorkspaceFile(workspace, false)
}

func readWorkspaceFile(workspace string, record bool) *WorkspaceFile {
	resolved := resolveWorkspaceForFile(workspace)
	path := WorkspaceFilePath(resolved)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	wf := &WorkspaceFile{Path: path, Workspace: resolved, Loopholes: jsonx.NewOrderedMap()}
	fixUnreadable := " Fix it, or delete it and run `yolo loopholes enable` or `disable` " +
		"for this workspace again, which writes a new one."
	if err != nil {
		wf.unreadable = true
		wf.Problems = append(wf.Problems, path+": cannot read this per-workspace file: "+err.Error()+"."+fixUnreadable)
		return wf
	}
	parsed, err := json5.Decode(data)
	if err != nil {
		wf.unreadable = true
		wf.Problems = append(wf.Problems, path+": cannot parse this per-workspace file: "+err.Error()+"."+fixUnreadable)
		return wf
	}
	m, ok := asMap(parsed)
	if !ok {
		wf.unreadable = true
		wf.Problems = append(wf.Problems, path+": this per-workspace file must hold one JSON object."+fixUnreadable)
		return wf
	}
	f := &srcFile{path: path, data: data}
	if record {
		wf.node = fileTree(f, m)
	}
	// Every problem is located in the file whether or not the caller records provenance: the
	// refusal is the message a reader acts on.
	at := func(steps ...json5.Step) string { return srcOrigin{file: f, steps: steps}.String() }

	for _, k := range m.Keys() {
		if _, known := knownWorkspaceFileKeys[k]; !known {
			wf.Problems = append(wf.Problems, at(json5.Key(k))+": "+pytext.Repr(k)+
				" is not a key a per-workspace file holds; it holds \"workspace\" and \"loopholes\" "+
				"alone. Remove it.")
		}
	}

	nv, _ := m.Get(workspaceFileWorkspaceKey)
	named, _ := nv.(string)
	wf.Named = named
	wf.Applies = named != "" && brokeredWorkspaceKeyProblem(named) == "" && brokeredKeyNames(named, resolved)

	lv, present := m.Get(workspaceFileLoopholesKey)
	if !present || lv == nil {
		return wf
	}
	block, ok := asMap(lv)
	if !ok {
		wf.Problems = append(wf.Problems, at(json5.Key(workspaceFileLoopholesKey))+
			": \"loopholes\" must be an object of loophole name -> {\"enabled\": true|false}. "+
			"Fix it, or run `yolo loopholes enable` or `disable` for this workspace after deleting it.")
		return wf
	}
	for _, name := range block.Keys() {
		ev, _ := block.Get(name)
		steps := []json5.Step{json5.Key(workspaceFileLoopholesKey), json5.Key(name)}
		if !hostServiceName.MatchString(name) {
			wf.Problems = append(wf.Problems, at(steps...)+": "+pytext.Repr(name)+
				" is not a loophole name (^[a-zA-Z][a-zA-Z0-9_-]{0,63}$). Remove it.")
			continue
		}
		entry, ok := asMap(ev)
		if !ok {
			wf.Problems = append(wf.Problems, at(steps...)+": expected {\"enabled\": true} or "+
				"{\"enabled\": false} (got "+pyReprValue(ev)+"). Fix it, or run "+
				"`yolo loopholes enable "+name+"` or `yolo loopholes disable "+name+"` for this workspace.")
			continue
		}
		bad := false
		for _, k := range entry.Keys() {
			if _, known := knownWorkspaceFileLoopholeKeys[k]; !known {
				bad = true
				wf.Problems = append(wf.Problems, at(append(steps, json5.Key(k))...)+": "+pytext.Repr(k)+
					" is not a key a per-workspace file holds for a loophole; it holds \"enabled\" "+
					"alone, and a loophole's other keys go in "+paths.UserConfigPath()+". Remove it.")
			}
		}
		v, has := entry.Get(workspaceFileEnabledKey)
		if !has {
			continue
		}
		b, isBool := v.(bool)
		if !isBool {
			wf.Problems = append(wf.Problems, at(append(steps, json5.Key(workspaceFileEnabledKey))...)+
				": \"enabled\" must be true or false (got "+pyReprValue(v)+"). Run "+
				"`yolo loopholes enable "+name+"` or `yolo loopholes disable "+name+"` for this "+
				"workspace, which writes it.")
			continue
		}
		if !bad {
			wf.Loopholes.Set(name, b)
		}
	}
	return wf
}

// Mismatch is why a file that does not apply is ignored, with the next step; "" when it applies.
func (wf *WorkspaceFile) Mismatch() string {
	if wf == nil || wf.Applies || wf.unreadable {
		return ""
	}
	what := "names no workspace"
	if wf.Named != "" {
		what = "names the workspace " + pytext.Repr(wf.Named)
		if prob := brokeredWorkspaceKeyProblem(wf.Named); prob != "" {
			what += ", which is refused: " + prob
		}
	}
	return wf.Path + " " + what + ", not " + wf.Workspace + ", so nothing in it applies. Delete " +
		"it, and run `yolo loopholes enable` or `disable` in " + wf.Workspace + " to write this " +
		"workspace's own."
}

// Switches describes the switches that apply, as "github-broker on, journal off", for the
// line that names the file.
func (wf *WorkspaceFile) Switches() string {
	if wf == nil || !wf.Applies || wf.Loopholes.Len() == 0 {
		return ""
	}
	parts := make([]string, 0, wf.Loopholes.Len())
	for _, name := range wf.Loopholes.Keys() {
		v, _ := wf.Loopholes.Get(name)
		word := "off"
		if v == true {
			word = "on"
		}
		parts = append(parts, name+" "+word)
	}
	return strings.Join(parts, ", ")
}

// LoopholeSwitch is what this file says about one loophole's `enabled`, and whether it says
// anything: only a file that applies says anything.
func (wf *WorkspaceFile) LoopholeSwitch(name string) (enabled, set bool) {
	if wf == nil || !wf.Applies {
		return false, false
	}
	v, ok := wf.Loopholes.Get(name)
	if !ok {
		return false, false
	}
	return v == true, true
}

// projection is the part of the file that merges into a config: `loopholes.<name>.enabled` for
// each well-formed switch, nil when the file does not apply or holds none.
func (wf *WorkspaceFile) projection() *jsonx.OrderedMap {
	if wf == nil || !wf.Applies || wf.Loopholes.Len() == 0 {
		return nil
	}
	block := jsonx.NewOrderedMap()
	for _, name := range wf.Loopholes.Keys() {
		v, _ := wf.Loopholes.Get(name)
		entry := jsonx.NewOrderedMap()
		entry.Set(workspaceFileEnabledKey, v)
		block.Set(name, entry)
	}
	out := jsonx.NewOrderedMap()
	out.Set(workspaceFileLoopholesKey, block)
	return out
}

// applyWorkspaceFile merges workspace's per-workspace file over a composed config, folding its
// provenance the same way so a refusal of a key it wrote names it.
func applyWorkspaceFile(cfg *jsonx.OrderedMap, node *srcNode, workspace string, record bool) (*jsonx.OrderedMap, *srcNode) {
	wf := readWorkspaceFile(workspace, record)
	proj := wf.projection()
	if proj == nil {
		return cfg, node
	}
	if cfg == nil {
		cfg = jsonx.NewOrderedMap()
	}
	return mergeConfig(cfg, proj, node, wf.node.without(workspaceFileWorkspaceKey))
}

// WithWorkspaceFile is cfg with workspace's per-workspace file merged over it, and the
// provenance beside it: for a caller that composes user and workspace config itself
// (`yolo check`), so it reads what LoadConfig reads.
func WithWorkspaceFile(cfg *jsonx.OrderedMap, src *Sources, workspace string) (*jsonx.OrderedMap, *Sources) {
	merged, node := applyWorkspaceFile(cfg, src.node(), workspace, src != nil)
	return merged, sourcesOf(node)
}

// validateWorkspaceFile reports the per-workspace file's refusals as errors, and a file that
// names another workspace as a warning, since nothing in it is used.
func validateWorkspaceFile(workspace string, errs, warns *[]string) {
	wf := ReadWorkspaceFile(workspace)
	if wf == nil {
		return
	}
	for _, p := range wf.Problems {
		add(errs, p)
	}
	if m := wf.Mismatch(); m != "" {
		add(warns, m)
	}
}

// SetWorkspaceLoophole writes one loophole switch into workspace's per-workspace file and returns
// the file. It keeps the file's other switches, creates the folder 0700 and the file 0600, and
// replaces the file in one rename, so a reader never sees half of it. It never touches the user
// config (OQ-BB12).
//
// A file it cannot read whole is refused rather than overwritten: it may hold a switch someone
// wrote by hand, and the message names it to fix or delete.
func SetWorkspaceLoophole(workspace, name string, enabled bool) (string, error) {
	if !hostServiceName.MatchString(name) {
		return "", fmt.Errorf("%s is not a loophole name (^[a-zA-Z][a-zA-Z0-9_-]{0,63}$)", pytext.Repr(name))
	}
	resolved := resolveWorkspaceForFile(workspace)
	wf, path, err := workspaceFileToRewrite(resolved)
	if err != nil {
		return path, err
	}
	block := jsonx.NewOrderedMap()
	if wf != nil {
		for _, k := range wf.Loopholes.Keys() {
			v, _ := wf.Loopholes.Get(k)
			block.Set(k, v)
		}
	}
	block.Set(name, enabled)
	return path, writeWorkspaceFile(path, wf.namedOr(resolved), block)
}

// RemoveWorkspaceLoophole takes name's switch out of workspace's per-workspace file, for a
// loophole no longer installed, whose switch no `disable` could otherwise clear: it returns the
// file and whether it held the switch. The file's other switches are kept; a file left with
// none is deleted. Like SetWorkspaceLoophole it refuses a file it cannot read whole, and never
// touches the user config.
func RemoveWorkspaceLoophole(workspace, name string) (path string, removed bool, err error) {
	resolved := resolveWorkspaceForFile(workspace)
	wf, path, err := workspaceFileToRewrite(resolved)
	if err != nil || wf == nil {
		return path, false, err
	}
	if _, set := wf.Loopholes.Get(name); !set {
		return path, false, nil
	}
	block := jsonx.NewOrderedMap()
	for _, k := range wf.Loopholes.Keys() {
		if k != name {
			v, _ := wf.Loopholes.Get(k)
			block.Set(k, v)
		}
	}
	if block.Len() == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return path, false, err
		}
		return path, true, nil
	}
	return path, true, writeWorkspaceFile(path, wf.namedOr(resolved), block)
}

// workspaceFileToRewrite is the resolved workspace's per-workspace file, read whole, and the path
// a write goes to; the file is nil when there is none. A file that does not read whole, or names
// another workspace, is refused with its next step, since a rewrite would drop what is in it.
func workspaceFileToRewrite(resolved string) (*WorkspaceFile, string, error) {
	wf := readWorkspaceFile(resolved, false)
	if wf == nil {
		return nil, WorkspaceFilePath(resolved), nil
	}
	switch {
	case len(wf.Problems) > 0:
		return nil, wf.Path, fmt.Errorf("%s\nyolo rewrites this file whole, so it does not write over "+
			"one it cannot read whole: fix it, or delete it, then run this again",
			strings.Join(wf.Problems, "\n"))
	case !wf.Applies:
		return nil, wf.Path, errors.New(wf.Mismatch())
	}
	return wf, wf.Path, nil
}

// namedOr is the `workspace` field a rewrite keeps: the one the file was written with, which its
// name was derived from, else resolved for a new file.
func (wf *WorkspaceFile) namedOr(resolved string) string {
	if wf != nil && wf.Named != "" {
		return wf.Named
	}
	return resolved
}

// writeWorkspaceFile writes a per-workspace file for workspace holding the switches in block
// (loophole name → enabled), whole, in one rename, the folder 0700 and the file 0600.
func writeWorkspaceFile(path, workspace string, block *jsonx.OrderedMap) error {
	loopholes := jsonx.NewOrderedMap()
	for _, k := range block.Keys() {
		v, _ := block.Get(k)
		entry := jsonx.NewOrderedMap()
		entry.Set(workspaceFileEnabledKey, v)
		loopholes.Set(k, entry)
	}
	doc := jsonx.NewOrderedMap()
	doc.Set(workspaceFileWorkspaceKey, workspace)
	doc.Set(workspaceFileLoopholesKey, loopholes)
	body, err := jsonx.DumpsIndent(doc, 2)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writePrivateAtomically(path, []byte(workspaceFileHeader+body+"\n"))
}

// writePrivateAtomically writes data to path through a 0600 temporary file in the same folder
// and one rename.
func writePrivateAtomically(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
