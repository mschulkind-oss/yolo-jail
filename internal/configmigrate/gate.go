package configmigrate

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ValidationError is a plan that proved equal but whose converted files the launch's config gate
// refuses (RC-D44): the equality proof compares what both layouts say, lowered the same way, and
// is blind to a spelling the readable reader then refuses. Nothing was touched.
type ValidationError struct {
	Plan *MigrationPlan
	// Problems are the launch's refusals of the converted files, each naming its file and line,
	// that the old files do not already meet.
	Problems []string
}

func (e *ValidationError) Error() string {
	return "the converted files would be refused by the next launch, so nothing was changed:\n  " +
		strings.Join(e.Problems, "\n  ") +
		"\nThis is a yolo bug: please report it with this output (`yolo config migrate --dry-run` shows the converted files). Your files still work as they are."
}

// runGate is the launch's config gate (config.RunMigrationGate) over one layout. A seam for the
// test that pins the call: deleting the gate must fail it.
var runGate = func(userConfigDir, workspace string) config.MigrationGate {
	return config.RunMigrationGate(userConfigDir, workspace, loopholes.NewResolver())
}

// gate stages the converted files off to the side, beside links to everything else the user
// config directory and the workspace hold, and runs the launch's config gate over that layout
// and over the files as they are. A refusal the converted layout meets and the current one does
// not is the plan's, and the plan is refused with it before any original is touched.
func (p *MigrationPlan) gate() error {
	g := p.Graph
	content := map[*File][]byte{}
	for _, f := range g.Files {
		content[f] = f.Bytes
	}
	mirror, err := os.MkdirTemp("", "yolo-config-migrate-gate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mirror)
	if resolved, err := filepath.EvalSymlinks(mirror); err == nil {
		mirror = resolved
	}
	// A converted file may spell an absolute path into the workspace or the user config
	// directory (an env file, an include). Staged, it points into the staged copy, as it will
	// point into the real one once the files are in place: a workspace file's env file must be
	// inside the workspace the gate reads.
	for _, t := range p.Targets {
		content[t.File] = relocate(t.Bytes, mirror, g.Request.Workspace, g.Request.UserConfigDir)
	}
	_, workspace, err := writeMirror(mirror, g, content)
	if err != nil {
		return fmt.Errorf("stage the converted files: %w; nothing was changed", err)
	}
	userDir := ""
	if g.Request.UserConfigDir != "" {
		userDir = filepath.Join(mirror, g.Request.UserConfigDir)
		if err := linkTheRest(g.Request.UserConfigDir, userDir, g); err != nil {
			return fmt.Errorf("stage the converted files: %w; nothing was changed", err)
		}
	}
	// The workspace's other entries are deliberately not linked in: the env-file scope check
	// resolves symlinks (envsourcescope.go), so a link to a real workspace file would read as a
	// file outside the staged workspace. A path that does not exist resolves by its deepest
	// existing ancestor, the staged workspace, which is where it will be.
	before := runGate(g.Request.UserConfigDir, g.Request.Workspace)
	after := runGate(userDir, workspace)
	already := map[string]int{}
	for _, msg := range before.Problems {
		already[unmirrorText(msg, mirror)]++
	}
	var problems []string
	for i, msg := range after.Problems {
		key := unmirrorText(msg, mirror)
		if already[key] > 0 {
			already[key]--
			continue
		}
		located := msg
		if i < len(after.Located) {
			located = after.Located[i]
		}
		problems = append(problems, unmirrorText(located, mirror))
	}
	p.Unchecked = after.Unchecked
	if g.Request.UserConfigDir != "" {
		renames, packProblems := packRenames(g.Request.UserConfigDir, userDir)
		p.Renames = renames
		problems = append(problems, packProblems...)
	}
	if len(problems) > 0 {
		return &ValidationError{Plan: p, Problems: problems}
	}
	return nil
}

// loadPacks is the launch's pack selection read from the user config in dir. A seam only in that
// it names the one reader both sides of the proof share.
func loadPacks(dir string) ([]config.PackEntry, error) {
	defer paths.ReadUserConfigFrom(dir)()
	return config.LoadPacks(func(string) {})
}

// packRenames proves the converted user config selects the very packs the old one does, by what
// the launch reads (config.LoadPacks), and returns the names that change. The equality proof
// lowers both sides through one converter, so it cannot see a pack renamed by its readable
// handle; this compares each entry's identity — its physical directory, its repository, path and
// ref, or the shipped pack it names — and lets only the name differ.
func packRenames(userConfigDir, stagedUserDir string) ([]PackRename, []string) {
	before, err := loadPacks(userConfigDir)
	if err != nil {
		return nil, nil // the old selection does not read; the launch's own gate says so
	}
	after, err := loadPacks(stagedUserDir)
	if err != nil {
		return nil, []string{"the converted `packs` do not read: " + err.Error()}
	}
	byIdentity := map[string][]config.PackEntry{}
	for _, e := range after {
		id := packIdentity(e)
		byIdentity[id] = append(byIdentity[id], e)
	}
	var renames []PackRename
	var problems []string
	for _, e := range before {
		id := packIdentity(e)
		match := byIdentity[id]
		if len(match) == 0 {
			problems = append(problems, fmt.Sprintf("the converted `packs` no longer select the pack %s (%s)", e.Name, e.Source))
			continue
		}
		n := match[0]
		byIdentity[id] = match[1:]
		if n.Name != e.Name {
			renames = append(renames, PackRename{Old: e.Name, New: n.Name, OldSource: e.Source, NewSource: n.Source})
		}
	}
	for _, rest := range byIdentity {
		for _, e := range rest {
			problems = append(problems, fmt.Sprintf("the converted `packs` select a pack the old ones do not: %s (%s)", e.Name, e.Source))
		}
	}
	return renames, problems
}

// packIdentity is what makes two `packs` entries the same pack whatever they are named.
func packIdentity(e config.PackEntry) string {
	filter := "|only=" + strings.Join(e.Only, ",") + "|exclude=" + strings.Join(e.Exclude, ",")
	switch {
	case e.IsEmbedded:
		name := e.EmbeddedName
		if name == "" {
			name = e.Name
		}
		return "shipped:" + name + filter
	case e.PhysicalPath != "":
		return "dir:" + e.PhysicalPath + filter
	}
	if u, err := url.Parse(e.Source); err == nil && u.Scheme == "file" {
		dir := u.Path
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		return "dir:" + filepath.Clean(dir) + filter
	}
	a, err := e.PackAddress()
	if err != nil || a.Repo == "" {
		return "source:" + e.Source + filter
	}
	repo := strings.TrimSuffix(strings.TrimSuffix(a.Repo, "/"), ".git")
	return "git:" + repo + "//" + strings.Trim(a.Path, "/") + "?" + a.Ref + filter
}

func unmirrorText(s, mirror string) string { return strings.ReplaceAll(s, mirror, "") }

// relocate rewrites each absolute dir in b, where it ends a quoted path or continues it, to the
// same path under mirror. Longer dirs go first, so a dir inside another moves once.
func relocate(b []byte, mirror string, dirs ...string) []byte {
	var keep []string
	for _, d := range dirs {
		if filepath.IsAbs(d) && d != "/" {
			keep = append(keep, filepath.Clean(d))
		}
	}
	if len(keep) == 2 && len(keep[1]) > len(keep[0]) {
		keep[0], keep[1] = keep[1], keep[0]
	}
	s := string(b)
	for _, d := range keep {
		var out strings.Builder
		for {
			i := strings.Index(s, d)
			if i < 0 {
				out.WriteString(s)
				break
			}
			end := i + len(d)
			boundary := end == len(s) || s[end] == '/' || s[end] == '"' || s[end] == '\''
			startOK := i == 0 || s[i-1] == '"' || s[i-1] == '\'' || s[i-1] == ' ' || s[i-1] == '='
			out.WriteString(s[:i])
			if boundary && startOK && !strings.HasPrefix(d, mirror) {
				out.WriteString(mirror)
			}
			out.WriteString(d)
			s = s[end:]
		}
		s = out.String()
	}
	return []byte(s)
}

// linkTheRest links every entry of the real directory dir that the staged copy at staged does not
// hold, so the gate reads the per-workspace files, packs and layers a launch would: everything but
// the old files the conversion replaces.
func linkTheRest(dir, staged string, g *Graph) error {
	replaced := map[string]bool{}
	for _, f := range g.Files {
		if f.Role == RoleConvert && filepath.Dir(f.Logical) == dir {
			replaced[filepath.Base(f.Logical)] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(staged, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if replaced[name] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(staged, name)); err == nil {
			continue
		}
		if err := os.Symlink(filepath.Join(dir, name), filepath.Join(staged, name)); err != nil {
			return err
		}
	}
	return nil
}
