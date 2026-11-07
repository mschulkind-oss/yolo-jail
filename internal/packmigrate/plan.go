package packmigrate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// Options are a migration's choices.
type Options struct {
	// KeepInstallHints keeps every legacy install hint on its own manager line
	// (packdecl.EncodeOptions); shipped packs use it so each hint keeps its source citation.
	KeepInstallHints bool
}

// Move is one file the migration relocates, both paths relative to the pack root.
type Move struct {
	From, To string
}

// KeptFiles is a `files` contribution the migration leaves declared, and why it is not a plain
// copy home/ can say.
type KeptFiles struct {
	Index  int
	From   string
	Reason string
}

// Migration is a planned, proven, not-yet-applied pack migration.
type Migration struct {
	Dir string
	// Legacy is the manifest file being replaced (pack.json or pack.jsonc).
	Legacy string
	// Manifest is the new pack.toml's bytes.
	Manifest []byte
	// Moves are the plain-copy files, one per file, into home/.
	Moves []Move
	// Folded are the contribution indexes the moves replace.
	Folded []int
	// Kept are the files contributions left declared.
	Kept []KeptFiles
	// Comments is every legacy comment, as carried into the new manifest, in document order.
	Comments []string
	// Loopholes are the pack's loophole modules rewritten from manifest.jsonc to loophole.toml
	// (RC-D28). A pack that already holds pack.toml still migrates these: Manifest is then nil.
	Loopholes []LoopholeMigration
	// ProfileFolds are the legacy `[[profile]]` entries folded into their providers, as an alias
	// or a variant (RC-D69); KeptProfiles the ones left legacy, each with why.
	ProfileFolds []packdecl.ProfileFold
	KeptProfiles []packdecl.KeptProfile

	// inPlace: the manifest is pack.toml itself, rewritten where it is (a legacy provider
	// spelling, RC-D69), rather than a legacy pack.json replaced by a new pack.toml.
	inPlace     bool
	legacyBytes []byte
	delivery    map[string]DeliveredFile
}

// Collision is one destination the migration would overwrite.
type Collision struct {
	Path   string // pack-relative
	Reason string
}

// CollisionError refuses a migration whose destinations are taken, before anything is written.
type CollisionError struct {
	Dir        string
	Collisions []Collision
}

func (e *CollisionError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: the migration would overwrite %d path(s), so nothing was changed:", e.Dir, len(e.Collisions))
	for _, c := range e.Collisions {
		fmt.Fprintf(&b, "\n  %s: %s", c.Path, c.Reason)
	}
	fmt.Fprintf(&b, "\nMove or remove those paths, then run `yolo pack migrate %s` again.", e.Dir)
	return b.String()
}

// conventionalDirs are the pack folders a convention reads; a files entry overlapping one is
// not moved, since the move would change what the convention finds.
var conventionalDirs = []string{"home", "skills", "briefing", "overlays", "patches"}

// Plan reads the legacy pack at dir and returns its migration, proven equal (manifest accessors,
// delivered content and modes, posture semantics) before anything is written. It refuses a pack
// that is already grouped, has no manifest, does not read cleanly, holds a value grouped TOML
// cannot say (packdecl.UnrepresentableError), or whose destinations are taken (CollisionError).
func Plan(dir string, opts Options) (*Migration, error) {
	if _, err := os.Lstat(filepath.Join(dir, packdecl.ManifestTOMLName)); err == nil {
		if data, pending := providerSpellingsPending(dir); pending {
			return planProviderSpellings(dir, data)
		}
		return planLoopholesOnly(dir)
	}
	legacyName, legacyBytes, err := legacyManifest(dir)
	if err != nil {
		return nil, err
	}
	old, problems := packdecl.Decode(legacyBytes)
	if len(problems) != 0 || old == nil {
		return nil, fmt.Errorf("%s: %s does not read cleanly, so it cannot be proven equal after migration:\n  %s\nFix those, check with `yolo pack lint %s`, then migrate", dir, legacyName, strings.Join(problems, "\n  "), dir)
	}
	doc, err := configdoc.Parse(legacyBytes, configdoc.JSONC)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", dir, legacyName, err)
	}
	oldPack, problems := packload.LoadDir(dir, "")
	if len(problems) != 0 || oldPack == nil {
		return nil, fmt.Errorf("%s: the pack does not load:\n  %s\nFix those, check with `yolo pack lint %s`, then migrate", dir, strings.Join(problems, "\n  "), dir)
	}
	m := &Migration{Dir: dir, Legacy: legacyName, legacyBytes: legacyBytes}
	raw, _ := doc.Values.Get("contributes")
	items, _ := raw.([]any)
	if len(items) != len(old.Contributes) {
		return nil, fmt.Errorf("%s: %s has %d contributions but decodes to %d; refusing to migrate a manifest the reader does not map one to one", dir, legacyName, len(items), len(old.Contributes))
	}
	folded := map[int]bool{}
	var collisions []Collision
	destinations := map[string]string{}
	for i, item := range items {
		fields, ok := item.(*jsonx.OrderedMap)
		if !ok || old.Contributes[i].Kind != packdecl.KindFiles {
			continue
		}
		moves, reason := plainCopy(dir, i, fields, items)
		if reason != "" {
			from, _ := fields.Get("from")
			fromText, _ := from.(string)
			m.Kept = append(m.Kept, KeptFiles{Index: i, From: fromText, Reason: reason})
			continue
		}
		folded[i] = true
		m.Folded = append(m.Folded, i)
		for _, move := range moves {
			if previous, taken := destinations[move.To]; taken {
				collisions = append(collisions, Collision{Path: move.To, Reason: "both " + previous + " and " + move.From + " would move here"})
				continue
			}
			destinations[move.To] = move.From
			m.Moves = append(m.Moves, move)
		}
	}
	collisions = append(collisions, destinationCollisions(dir, m.Moves)...)
	for i, c := range old.Contributes {
		if folded[i] || c.Kind != packdecl.KindFiles || c.Into == "" {
			continue
		}
		for _, move := range m.Moves {
			if landing := strings.TrimPrefix(move.To, "home/"); landing == c.Into || strings.HasPrefix(landing, c.Into+"/") {
				collisions = append(collisions, Collision{Path: move.To, Reason: fmt.Sprintf("lands inside ~/%s, which contribution %d still declares", c.Into, i)})
			}
		}
	}
	if len(collisions) != 0 {
		return nil, &CollisionError{Dir: dir, Collisions: collisions}
	}
	if m.delivery, err = Delivery(oldPack); err != nil {
		return nil, fmt.Errorf("%s: reading what the pack delivers: %w", dir, err)
	}

	reduced, origIndex := withoutContributions(doc.Values, folded)
	lines, err := packdecl.EncodeLegacyTOMLLines(reduced, packdecl.EncodeOptions{KeepInstallHints: opts.KeepInstallHints})
	if err != nil {
		var unrepresentable *packdecl.UnrepresentableError
		if errors.As(err, &unrepresentable) {
			unrepresentable.Path = remapLabel(unrepresentable.Path, origIndex)
			return nil, fmt.Errorf("%s: %s: %w", dir, legacyName, unrepresentable)
		}
		return nil, fmt.Errorf("%s: %s: %w", dir, legacyName, err)
	}
	for i := range lines {
		lines[i].Source = remapPath(lines[i].Source, origIndex)
	}
	lines, m.Comments = placeComments(lines, doc)
	m.Manifest = packdecl.JoinTOMLLines(lines)

	got, problems := packdecl.DecodeTOML(m.Manifest)
	if len(problems) != 0 {
		return nil, fmt.Errorf("%s: the encoded pack.toml does not read back (a yolo bug; nothing was changed):\n  %s\nThe pack keeps its %s; report this output as a yolo issue", dir, strings.Join(problems, "\n  "), legacyName)
	}
	want := *old
	want.Contributes = nil
	for i, c := range old.Contributes {
		if !folded[i] {
			want.Contributes = append(want.Contributes, c)
		}
	}
	if diffs := ManifestDifferences(&want, got); len(diffs) != 0 {
		return nil, fmt.Errorf("%s: the encoded pack.toml would change what the pack does, so nothing was changed:\n  %s", dir, strings.Join(diffs, "\n  "))
	}
	// The encoder writes every provider keyed; a legacy profile then folds into its provider,
	// the same rewrite a pack.toml still writing one takes (RC-D69), proven by the rewrite itself.
	rewrite, err := RewriteProviderSpellings(m.Manifest)
	if err != nil {
		return nil, fmt.Errorf("%s: folding %s's profiles into their providers (a yolo bug; nothing was changed): %w", dir, legacyName, err)
	}
	if rewrite != nil {
		m.Manifest, m.ProfileFolds, m.KeptProfiles = rewrite.Manifest, rewrite.Folds, rewrite.Kept
	}
	if m.Loopholes, err = planLoopholes(dir, oldPack.Decl.LoopholeSources()); err != nil {
		return nil, err
	}
	return m, nil
}

// planLoopholesOnly is the migration of a pack that already holds pack.toml: only its loophole
// modules still on a legacy manifest.jsonc are rewritten.
func planLoopholesOnly(dir string) (*Migration, error) {
	pack, problems := packload.LoadDir(dir, "")
	if len(problems) != 0 || pack == nil {
		return nil, fmt.Errorf("%s: the pack does not load:\n  %s\nFix those, check with `yolo pack lint %s`, then migrate", dir, strings.Join(problems, "\n  "), dir)
	}
	loopholes, err := planLoopholes(dir, pack.Decl.LoopholeSources())
	if err != nil {
		return nil, err
	}
	if len(loopholes) == 0 {
		return nil, fmt.Errorf("%s already has %s; there is nothing to migrate", dir, packdecl.ManifestTOMLName)
	}
	return &Migration{Dir: dir, Loopholes: loopholes}, nil
}

func legacyManifest(dir string) (string, []byte, error) {
	if _, err := os.Lstat(filepath.Join(dir, packdecl.ManifestTOMLName)); err == nil {
		return "", nil, fmt.Errorf("%s already has %s; there is nothing to migrate", dir, packdecl.ManifestTOMLName)
	}
	var found []string
	for _, name := range []string{packdecl.ManifestLegacyJSONCName, packdecl.ManifestLegacyJSONName} {
		if info, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			if !info.Mode().IsRegular() {
				return "", nil, fmt.Errorf("%s: %s is not a regular file; replace the link with the file it names, then migrate", dir, name)
			}
			found = append(found, name)
		}
	}
	switch len(found) {
	case 0:
		return "", nil, fmt.Errorf("%s has no %s or %s; a pack without a manifest needs no migration", dir, packdecl.ManifestLegacyJSONName, packdecl.ManifestLegacyJSONCName)
	case 2:
		return "", nil, fmt.Errorf("%s has both %s and %s; the reader uses %s, so remove the other, then migrate", dir, found[0], found[1], found[0])
	}
	data, err := os.ReadFile(filepath.Join(dir, found[0]))
	if err != nil {
		return "", nil, err
	}
	return found[0], data, nil
}

// plainCopy decides whether contribution i is a copy home/ can say: packload's home discovery
// delivers each regular file under home/<into> to ~/<into> and nothing else, so the entry must
// name only from and into, both clean relative paths, from a tree of regular files no other
// declaration or convention reads. It returns the per-file moves, or why the entry stays.
func plainCopy(dir string, index int, fields *jsonx.OrderedMap, items []any) ([]Move, string) {
	for _, key := range fields.Keys() {
		if key != "kind" && key != "from" && key != "into" {
			return nil, fmt.Sprintf("it sets %q, which home/ cannot say", key)
		}
	}
	fromValue, _ := fields.Get("from")
	intoValue, _ := fields.Get("into")
	from, _ := fromValue.(string)
	into, _ := intoValue.(string)
	if from == "" || into == "" {
		return nil, "it does not name both from and into"
	}
	if !cleanRelative(from) || !cleanRelative(into) {
		return nil, "from or into is not a clean relative path"
	}
	for _, convention := range conventionalDirs {
		if overlaps(from, convention) {
			return nil, fmt.Sprintf("its source overlaps the conventional %s/ folder", convention)
		}
	}
	for _, name := range []string{packdecl.ManifestLegacyJSONName, packdecl.ManifestLegacyJSONCName, packdecl.ManifestTOMLName, "README.md"} {
		if overlaps(from, name) {
			return nil, fmt.Sprintf("its source overlaps %s", name)
		}
	}
	for j, item := range items {
		other, ok := item.(*jsonx.OrderedMap)
		if !ok || j == index {
			continue
		}
		for _, key := range other.Keys() {
			value, _ := other.Get(key)
			if text, ok := value.(string); ok && (key == "from" || key == "patches") && cleanRelative(text) && overlaps(from, text) {
				return nil, fmt.Sprintf("contribution %d also reads %s", j, text)
			}
		}
	}
	source := filepath.Join(dir, filepath.FromSlash(from))
	info, err := os.Lstat(source)
	if err != nil {
		return nil, fmt.Sprintf("its source cannot be read: %v", err)
	}
	if info.Mode().IsRegular() {
		return []Move{{From: from, To: path.Join("home", into)}}, ""
	}
	if !info.IsDir() {
		return nil, "its source is not a regular file or directory"
	}
	var moves []Move
	reason := ""
	err = filepath.WalkDir(source, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == ".git" {
			reason = "its tree holds a .git entry, which home/ discovery skips"
			return filepath.SkipAll
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			reason = "its tree holds a link or special file, which home/ delivers differently"
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(source, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		moves = append(moves, Move{From: path.Join(from, rel), To: path.Join("home", into, rel)})
		return nil
	})
	if err != nil {
		return nil, fmt.Sprintf("its tree cannot be read: %v", err)
	}
	if reason != "" {
		return nil, reason
	}
	if len(moves) == 0 {
		return nil, "its tree holds no file"
	}
	return moves, ""
}

func cleanRelative(p string) bool {
	return p != "" && p != "." && path.Clean(p) == p && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../") && !strings.HasPrefix(p, "~")
}

// overlaps reports whether one path is the other or lies inside it.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// destinationCollisions refuses a destination that exists, or whose parent is not a directory.
func destinationCollisions(dir string, moves []Move) []Collision {
	var out []Collision
	for _, move := range moves {
		target := filepath.Join(dir, filepath.FromSlash(move.To))
		if _, err := os.Lstat(target); err == nil {
			out = append(out, Collision{Path: move.To, Reason: "already exists (moving " + move.From + " here would overwrite it)"})
			continue
		}
		for parent := path.Dir(move.To); parent != "." && parent != "/"; parent = path.Dir(parent) {
			info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(parent)))
			if err == nil && !info.IsDir() {
				out = append(out, Collision{Path: parent, Reason: "is not a directory, and " + move.From + " would move beneath it"})
				break
			}
		}
	}
	return out
}

// withoutContributions copies the legacy tree minus the folded contributions, returning the
// original index of each kept one.
func withoutContributions(tree *jsonx.OrderedMap, folded map[int]bool) (*jsonx.OrderedMap, []int) {
	out := jsonx.NewOrderedMap()
	var origIndex []int
	for _, key := range tree.Keys() {
		value, _ := tree.Get(key)
		if key != "contributes" {
			out.Set(key, value)
			continue
		}
		items, _ := value.([]any)
		kept := make([]any, 0, len(items))
		for i, item := range items {
			if !folded[i] {
				kept = append(kept, item)
				origIndex = append(origIndex, i)
			}
		}
		if len(kept) != 0 {
			out.Set(key, kept)
		}
	}
	return out, origIndex
}

func remapPath(source []configdoc.Step, origIndex []int) []configdoc.Step {
	if len(source) < 2 || source[0].Key != "contributes" || !source[1].IsIndex || source[1].Index >= len(origIndex) {
		return source
	}
	out := append([]configdoc.Step(nil), source...)
	out[1].Index = origIndex[source[1].Index]
	return out
}

func remapLabel(label string, origIndex []int) string {
	var index int
	var rest string
	if n, err := fmt.Sscanf(label, "contributes[%d]", &index); err != nil || n != 1 || index >= len(origIndex) {
		return label
	}
	prefix := fmt.Sprintf("contributes[%d]", index)
	rest = strings.TrimPrefix(label, prefix)
	return fmt.Sprintf("contributes[%d]%s", origIndex[index], rest)
}

// placeComments carries every legacy comment, in document order, to just before the first line
// written from a key that follows it in the old file — or to the end when none does
// (readable-config-plan.md §6: delimiters change to #, nothing is attached by inference, nothing
// is dropped).
func placeComments(lines []packdecl.TOMLLine, doc *configdoc.Document) ([]packdecl.TOMLLine, []string) {
	spans := doc.Index.Comments()
	if len(spans) == 0 {
		return lines, nil
	}
	offsets := make([]int, len(lines))
	for i, line := range lines {
		offsets[i] = sourceOffset(doc, line.Source)
	}
	before := map[int][][]string{}
	var trailing [][]string
	var carried []string
	for _, span := range spans {
		text := commentLines(doc.Bytes[span.Start:span.End])
		carried = append(carried, strings.Join(text, "\n"))
		target := -1
		for i, offset := range offsets {
			if offset >= span.End && (target < 0 || offset < offsets[target]) {
				target = i
			}
		}
		if target < 0 {
			trailing = append(trailing, text)
			continue
		}
		before[target] = append(before[target], text)
	}
	var out []packdecl.TOMLLine
	for i, line := range lines {
		indent := line.Text[:len(line.Text)-len(strings.TrimLeft(line.Text, " "))]
		for _, block := range before[i] {
			for _, text := range block {
				out = append(out, packdecl.TOMLLine{Text: indent + text})
			}
		}
		out = append(out, line)
	}
	for _, block := range trailing {
		for _, text := range block {
			out = append(out, packdecl.TOMLLine{Text: text})
		}
	}
	return out, carried
}

func sourceOffset(doc *configdoc.Document, source []configdoc.Step) int {
	if len(source) == 0 {
		return -1
	}
	var span configdoc.Span
	var ok bool
	var err error
	if source[len(source)-1].IsIndex {
		span, ok, err = doc.Index.Item(source...)
	} else {
		span, ok, err = doc.Index.Member(source...)
	}
	if err != nil || !ok {
		return -1
	}
	return span.Start
}

// commentLines turns one // or /* */ comment into # lines.
func commentLines(raw []byte) []string {
	text := string(bytes.TrimSpace(raw))
	if strings.HasPrefix(text, "//") {
		return []string{"#" + strings.TrimPrefix(text, "//")}
	}
	text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if line == "" {
			out = append(out, "#")
			continue
		}
		out = append(out, "# "+line)
	}
	for len(out) > 1 && out[0] == "#" {
		out = out[1:]
	}
	for len(out) > 1 && out[len(out)-1] == "#" {
		out = out[:len(out)-1]
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
