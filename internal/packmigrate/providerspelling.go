package packmigrate

// providerspelling.go is `yolo pack migrate` on a pack.toml that still writes a provider in a
// legacy spelling (docs/design/readable-config.md RC-D69): `[[provider]] name = "x"` becomes
// `[providers.x]`, and each `[[profile]]` over a provider the same manifest declares folds into
// it (packdecl.Manifest.FoldProfiles) — an alias, or a variant with that `name` — with
// `formerly.profiles` moved to `formerly.providers`. A manifest declaring one id both ways has
// the legacy item's keys moved under the keyed table, where TOML's duplicate-key rule refuses
// any key both write.
//
// It is a text rewrite over byte spans (configdoc's TOML index), so every comment and every line
// the rewrite does not name stays where it was. Like every migration it PROVES EQUALITY OR
// WRITES NOTHING: the rewritten bytes must decode to the folded manifest
// (ManifestDifferences), and the pack must deliver the same files.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// textEdit replaces data[start:end] with text.
type textEdit struct {
	start, end int
	text       string
}

func applyEdits(data []byte, edits []textEdit) ([]byte, error) {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var b strings.Builder
	at := 0
	for _, e := range edits {
		if e.start < at {
			return nil, fmt.Errorf("two rewrites overlap at byte %d", e.start)
		}
		b.Write(data[at:e.start])
		b.WriteString(e.text)
		at = e.end
	}
	b.Write(data[at:])
	return []byte(b.String()), nil
}

func lineStartAt(data []byte, offset int) int {
	for offset > 0 && data[offset-1] != '\n' {
		offset--
	}
	return offset
}

// lineAfter is the offset just past the newline ending the line holding offset.
func lineAfter(data []byte, offset int) int {
	for offset < len(data) && data[offset] != '\n' {
		offset++
	}
	if offset < len(data) {
		offset++
	}
	return offset
}

// contentEnd is the offset just past the last line in [start, end) that is neither blank nor a
// comment: where a table's own lines stop and the next one's leading comments start.
func contentEnd(data []byte, start, end int) int {
	last := start
	for at := start; at < end; {
		next := lineAfter(data, at)
		if next > end {
			next = end
		}
		trimmed := strings.TrimSpace(string(data[at:next]))
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			last = next
		}
		at = next
	}
	return last
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(name string) string {
	if bareKey.MatchString(name) {
		return name
	}
	return strconv.Quote(name)
}

func tomlNames(names []string) string {
	if len(names) == 1 {
		return strconv.Quote(names[0])
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func tomlNameMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = tomlKey(k) + " = " + strconv.Quote(m[k])
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// formerlyInline is f as one inline table, the spelling the shipped packs use.
func formerlyInline(f *packdecl.Formerly) string {
	var parts []string
	if len(f.Packs) != 0 {
		quoted := make([]string, len(f.Packs))
		for i, p := range f.Packs {
			quoted[i] = strconv.Quote(p)
		}
		parts = append(parts, "packs = ["+strings.Join(quoted, ", ")+"]")
	}
	if len(f.Providers) != 0 {
		parts = append(parts, "providers = "+tomlNameMap(f.Providers))
	}
	if len(f.Profiles) != 0 {
		parts = append(parts, "profiles = "+tomlNameMap(f.Profiles))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func variantLines(id, key string, v packdecl.ProviderVariant) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "\n[providers.%s.variants.%s]\n", tomlKey(id), tomlKey(key))
	if v.Name != "" {
		fmt.Fprintf(&b, "name = %s\n", strconv.Quote(v.Name))
	}
	if v.Model != "" {
		fmt.Fprintf(&b, "model = %s\n", strconv.Quote(v.Model))
	}
	if v.Via != "" {
		fmt.Fprintf(&b, "via = %s\n", strconv.Quote(v.Via))
	}
	if v.EnforceModels != nil {
		fmt.Fprintf(&b, "enforce_models = %t\n", *v.EnforceModels)
	}
	return b.String(), nil
}

func kstep(key string) configdoc.Step { return configdoc.Step{Key: key} }
func kelem(i int) configdoc.Step      { return configdoc.Step{Index: i, IsIndex: true} }

// providerItems is each `[[provider]]` item's name, in document order.
func providerItems(doc *configdoc.Document, array string) []string {
	raw, _ := doc.Values.Get(array)
	items, _ := raw.([]any)
	names := make([]string, len(items))
	for i, item := range items {
		if fields, ok := item.(*jsonx.OrderedMap); ok {
			value, _ := fields.Get("name")
			names[i], _ = value.(string)
		}
	}
	return names
}

func keyedProviders(doc *configdoc.Document) map[string]bool {
	out := map[string]bool{}
	raw, _ := doc.Values.Get("providers")
	if table, ok := raw.(*jsonx.OrderedMap); ok {
		for _, id := range table.Keys() {
			out[id] = true
		}
	}
	return out
}

// subHeaders rewrites each `[provider.…]` / `[[provider.…]]` header line inside [start, end) to
// sit under prefix (`providers.<id>`).
func subHeaders(data []byte, start, end int, prefix string) []textEdit {
	var edits []textEdit
	for at := lineAfter(data, start); at < end; {
		next := lineAfter(data, at)
		if next > end {
			next = end
		}
		line := string(data[at:next])
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		for _, open := range []string{"[[provider.", "[provider."} {
			if strings.HasPrefix(trimmed, open) {
				brackets := strings.Repeat("[", strings.Count(open, "["))
				edits = append(edits, textEdit{at + indent, at + indent + len(open), brackets + prefix + "."})
				break
			}
		}
		at = next
	}
	return edits
}

// mergeBothWays moves each legacy `[[provider]]` item whose id also has a `[providers.<id>]`
// table under that table, so the result declares the id once. It returns data unchanged when no
// id is declared both ways.
func mergeBothWays(data []byte) ([]byte, error) {
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		return nil, err
	}
	keyed := keyedProviders(doc)
	var edits []textEdit
	for k, id := range providerItems(doc, "provider") {
		if id == "" || !keyed[id] {
			continue
		}
		item, ok, _ := doc.Index.Item(kstep("provider"), kelem(k))
		table, okTable, _ := doc.Index.Table(kstep("providers"), kstep(id))
		if !ok || !okTable {
			return nil, fmt.Errorf("cannot locate both declarations of provider %q", id)
		}
		end := contentEnd(data, item.Start, item.End)
		body := lineAfter(data, item.Start)
		// The item's own keys (before its first sub-table), less its name, go right under the
		// keyed table's header; its sub-tables go after the keyed table's last line.
		ownEnd := end
		for at := body; at < end; at = lineAfter(data, at) {
			if strings.HasPrefix(strings.TrimLeft(string(data[at:lineAfter(data, at)]), " \t"), "[") {
				ownEnd = at
				break
			}
		}
		var own strings.Builder
		if name, has, _ := doc.Index.Member(kstep("provider"), kelem(k), kstep("name")); has {
			nameLine, nameEnd := lineStartAt(data, name.Start), lineAfter(data, name.End)
			own.Write(data[body:nameLine])
			own.Write(data[nameEnd:ownEnd])
		} else {
			own.Write(data[body:ownEnd])
		}
		sub, err := applyEdits(data[ownEnd:end], shift(subHeaders(data, ownEnd-1, end, "providers."+tomlKey(id)), -ownEnd))
		if err != nil {
			return nil, err
		}
		header := lineAfter(data, table.Start)
		edits = append(edits, textEdit{header, header, own.String()})
		if len(sub) != 0 {
			tail := contentEnd(data, table.Start, table.End)
			edits = append(edits, textEdit{tail, tail, "\n" + string(sub)})
		}
		edits = append(edits, textEdit{item.Start, eatBlank(data, end), ""})
	}
	if len(edits) == 0 {
		return data, nil
	}
	return applyEdits(data, edits)
}

func shift(edits []textEdit, by int) []textEdit {
	for i := range edits {
		edits[i].start += by
		edits[i].end += by
	}
	return edits
}

// eatBlank extends a deletion ending at end over one following blank line, so removing a table
// does not leave two blank lines where one separated it from its neighbours.
func eatBlank(data []byte, end int) int {
	next := lineAfter(data, end)
	if next > end && strings.TrimSpace(string(data[end:next])) == "" {
		return next
	}
	return end
}

// ProviderSpellingRewrite is the planned rewrite of a pack.toml's legacy provider spellings.
type ProviderSpellingRewrite struct {
	// Manifest is the rewritten bytes.
	Manifest []byte
	// Folds are the `[[profile]]` entries folded into their providers.
	Folds []packdecl.ProfileFold
	// Kept are the `[[profile]]` entries left legacy, each with why.
	Kept []packdecl.KeptProfile
	// Merged reports that some provider was declared both ways and is now declared once.
	Merged bool
}

// RewriteProviderSpellings rewrites data's legacy provider spellings and proves the result
// declares the same thing. It returns nil, nil when there is nothing to rewrite: no
// `[[provider]]` and no `[[profile]]` that folds.
func RewriteProviderSpellings(data []byte) (*ProviderSpellingRewrite, error) {
	merged, err := mergeBothWays(data)
	if err != nil {
		return nil, err
	}
	base, problems := packdecl.DecodeTOML(merged)
	if len(problems) != 0 || base == nil {
		if string(merged) != string(data) {
			return nil, fmt.Errorf("moving each provider declared both ways under its [providers.<id>] table "+
				"does not read back, most likely because both declarations set the same key:\n  %s\n"+
				"Delete the key from one of them, then migrate again", strings.Join(problems, "\n  "))
		}
		return nil, fmt.Errorf("pack.toml does not read cleanly:\n  %s", strings.Join(problems, "\n  "))
	}
	folds, kept := base.FoldProfiles()
	doc, err := configdoc.Parse(merged, configdoc.TOML)
	if err != nil {
		return nil, err
	}
	legacyItems := providerItems(doc, "provider")
	if len(legacyItems) == 0 && len(folds) == 0 {
		if string(merged) == string(data) {
			return nil, nil
		}
	}
	want := base.FoldedProfiles(folds)
	wantProviders := map[string]packdecl.ProviderContribution{}
	for _, p := range want.Providers() {
		wantProviders[p.Name] = p
	}
	oldProviders := map[string]packdecl.ProviderContribution{}
	for _, p := range base.Providers() {
		oldProviders[p.Name] = p
	}
	foldsByProvider := map[string][]packdecl.ProfileFold{}
	for _, f := range folds {
		foldsByProvider[f.Provider] = append(foldsByProvider[f.Provider], f)
	}

	var edits []textEdit
	// The folded profiles go. The comment lines directly above one describe it, so they move with
	// it, to sit above the alias or variant it becomes.
	profileNames := providerItems(doc, "profile")
	used := map[int]bool{}
	carried := map[string]string{}
	for _, f := range folds {
		for k, name := range profileNames {
			if name != f.Profile || used[k] {
				continue
			}
			used[k] = true
			item, ok, _ := doc.Index.Item(kstep("profile"), kelem(k))
			if !ok {
				return nil, fmt.Errorf("cannot locate [[profile]] %q", f.Profile)
			}
			start := commentBlockStart(merged, item.Start)
			carried[f.Profile] = string(merged[start:item.Start])
			edits = append(edits, textEdit{start, eatBlank(merged, contentEnd(merged, item.Start, item.End)), ""})
			break
		}
	}

	// addFolds writes provider id's new alias list and variant tables into the table at span,
	// whose header line starts at span.Start; aliasPath locates an existing alias member.
	addFolds := func(id string, span configdoc.Span, aliasPath []configdoc.Step, headerText string) {
		header := lineAfter(merged, span.Start)
		fs := foldsByProvider[id]
		aliasesChanged := len(wantProviders[id].Aliases) != len(oldProviders[id].Aliases)
		var aliasComments strings.Builder
		for _, f := range fs {
			if f.Variant == "" {
				aliasComments.WriteString(carried[f.Profile])
			}
		}
		insert := ""
		if aliasesChanged {
			line := "alias = " + tomlNames(wantProviders[id].Aliases)
			if existing, has, _ := doc.Index.Member(aliasPath...); has {
				at := lineStartAt(merged, existing.Start)
				edits = append(edits, textEdit{at, existing.End, aliasComments.String() + string(merged[at:existing.Start]) + line})
			} else {
				insert = aliasComments.String() + line + "\n"
			}
		}
		if headerText != "" {
			edits = append(edits, textEdit{span.Start, header, headerText + "\n" + insert})
		} else if insert != "" {
			edits = append(edits, textEdit{header, header, insert})
		}
		var variants strings.Builder
		for _, f := range fs {
			if f.Variant == "" {
				continue
			}
			text, _ := variantLines(id, f.Variant, f.Value)
			variants.WriteString("\n" + carried[f.Profile] + strings.TrimPrefix(text, "\n"))
		}
		if variants.Len() != 0 {
			tail := contentEnd(merged, span.Start, span.End)
			edits = append(edits, textEdit{tail, tail, variants.String()})
		}
	}

	for k, id := range legacyItems {
		item, ok, _ := doc.Index.Item(kstep("provider"), kelem(k))
		if !ok || id == "" {
			return nil, fmt.Errorf("cannot locate [[provider]] number %d's name", k+1)
		}
		key := tomlKey(id)
		if name, has, _ := doc.Index.Member(kstep("provider"), kelem(k), kstep("name")); has {
			edits = append(edits, textEdit{lineStartAt(merged, name.Start), lineAfter(merged, name.End), ""})
		}
		edits = append(edits, subHeaders(merged, item.Start, item.End, "providers."+key)...)
		addFolds(id, item, []configdoc.Step{kstep("provider"), kelem(k), kstep("alias")}, "[providers."+key+"]")
		delete(foldsByProvider, id)
	}
	for id := range foldsByProvider {
		table, ok, _ := doc.Index.Table(kstep("providers"), kstep(id))
		if !ok {
			return nil, fmt.Errorf("cannot locate [providers.%s]", id)
		}
		addFolds(id, table, []configdoc.Step{kstep("providers"), kstep(id), kstep("alias")}, "")
	}

	// formerly.profiles naming a folded profile moves to formerly.providers.
	if base.Formerly != nil && formerlyInline(base.Formerly) != formerlyInline(want.Formerly) {
		member, has, _ := doc.Index.Member(kstep("formerly"))
		table, hasTable, _ := doc.Index.Table(kstep("formerly"))
		switch {
		case has:
			edits = append(edits, textEdit{member.Start, member.End, "formerly = " + formerlyInline(want.Formerly)})
		case hasTable:
			edits = append(edits, textEdit{table.Start, contentEnd(merged, table.Start, table.End),
				"[formerly]\n" + strings.TrimSuffix(strings.TrimPrefix(strings.ReplaceAll(formerlyInline(want.Formerly), ", ", "\n"), "{ "), " }") + "\n"})
		default:
			return nil, fmt.Errorf("cannot locate formerly")
		}
	}

	out, err := applyEdits(merged, edits)
	if err != nil {
		return nil, err
	}
	out = []byte(strings.TrimRight(string(out), "\n") + "\n")
	got, problems := packdecl.DecodeTOML(out)
	if len(problems) != 0 || got == nil {
		return nil, fmt.Errorf("the rewritten pack.toml does not read back:\n  %s", strings.Join(problems, "\n  "))
	}
	if diffs := ManifestDifferences(want, got); len(diffs) != 0 {
		return nil, fmt.Errorf("the rewritten pack.toml would change what the pack declares:\n  %s", strings.Join(diffs, "\n  "))
	}
	for _, spelling := range got.LegacySpellings {
		if strings.HasPrefix(spelling, "[[provider]]") {
			return nil, fmt.Errorf("the rewritten pack.toml still writes %s", spelling)
		}
	}
	return &ProviderSpellingRewrite{Manifest: out, Folds: folds, Kept: kept, Merged: string(merged) != string(data)}, nil
}

// planProviderSpellings is the migration of a pack.toml still writing a legacy provider
// spelling: the rewrite, in place, plus any loophole module still on manifest.jsonc.
func planProviderSpellings(dir string, data []byte) (*Migration, error) {
	rewrite, err := RewriteProviderSpellings(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", dir, packdecl.ManifestTOMLName, err)
	}
	if rewrite == nil {
		return planLoopholesOnly(dir)
	}
	m := &Migration{Dir: dir, Legacy: packdecl.ManifestTOMLName, Manifest: rewrite.Manifest, legacyBytes: data,
		inPlace: true, ProfileFolds: rewrite.Folds, KeptProfiles: rewrite.Kept}
	// A pack declaring one id both ways does not load until it is rewritten; its delivered
	// files are then read from the rewritten manifest only, after the write.
	if !rewrite.Merged {
		pack, problems := packload.LoadDir(dir, "")
		if len(problems) != 0 || pack == nil {
			return nil, fmt.Errorf("%s: the pack does not load:\n  %s\nFix those, check with `yolo pack lint %s`, then migrate", dir, strings.Join(problems, "\n  "), dir)
		}
		if m.delivery, err = Delivery(pack); err != nil {
			return nil, fmt.Errorf("%s: reading what the pack delivers: %w", dir, err)
		}
		if m.Loopholes, err = planLoopholes(dir, pack.Decl.LoopholeSources()); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// providerSpellingsPending is whether the pack.toml at dir still writes a legacy provider
// spelling a migration would rewrite.
func providerSpellingsPending(dir string) ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(dir, packdecl.ManifestTOMLName))
	if err != nil {
		return nil, false
	}
	return data, len(packdecl.LegacyProviderSpellings(data)) != 0
}

// commentBlockStart is where the run of comment lines directly above the line at offset begins
// (offset itself when there is none).
func commentBlockStart(data []byte, offset int) int {
	start := offset
	for start > 0 {
		prev := lineStartAt(data, start-1)
		if !strings.HasPrefix(strings.TrimSpace(string(data[prev:start])), "#") {
			break
		}
		start = prev
	}
	return start
}
