package entrypoint

// hostmodellist.go is what one `yolo host --` launch composes IN-PROCESS from a program's own
// derives, over that launch's own wire tables, and hands the program without touching a file:
//
//   - the list half of a program's MODEL MENU (packdecl.ModelMenu;
//     docs/design/model-lists-and-pickers.md MM-D24): the list a jail's boot renders into the file
//     the pack's `model_menu.list` names;
//   - a program's LAUNCH SELECTION (packdecl.LaunchSelection, a term coined there; MM-D30): the
//     selection its config surface's derive composes, the rows it names and the further surfaces
//     it decides, which the launch hands the program as argv words or variables when a `-p` moves
//     it.
//
// WHY NOT THE FILES `yolo host apply` COULD RENDER. They are written for the configured profile
// alone (OQ-HC3), never for the launch's `-p`, and they can be older than the launch, since only a
// wrapped launch with `host_apply_on_launch` on renders first. So the list surface stays
// `notAtHost`, and both halves run their derives for the launch instead.
//
// ONE CODE PATH WITH THE JAIL (NC-D1): each derive is the pack's own, found by the surface whose
// path the declaration names, and it runs through deriveComputedLayer over the jail's own readers
// of the wire tables (hostSources, HC-D13). What differs is only where the tables come from, and
// that nothing is written.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/modelmenu"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// hostSurfaceLayer is the layer p's derive composes for its surface at the home-relative path rel,
// over in, the launch's inputs: the surface it ran for, and the layer (nil when the pack ships no
// derive for it). home is the real home the launch runs in; nothing is written into it. A derive's
// warnings are handed to warn, one line each; a nil warn discards them.
//
// An error means the layer could not be composed at all: p declares no surface at rel, its
// surfaces do not decode, or the derive failed.
func hostSurfaceLayer(p *packload.Pack, rel, home string, in *HostInputs,
	warn func(string)) (manifest.Surface, map[string]any, error) {
	surfaces, problems := p.SurfacesFor(render.ProfileFor(render.KindHost).AgentAutonomy)
	if len(problems) > 0 {
		return manifest.Surface{}, nil, fmt.Errorf("%s", problems[0])
	}
	want := "~/" + rel
	for _, s := range surfaces {
		if s.Path != want {
			continue
		}
		e := &Env{Home: home, Vars: in.vars(), hostTarget: true, Stderr: warnWriter(warn)}
		sources := newHostSources(e, in)
		layer, _, err := deriveComputedLayer(e, s, packload.DeriveScript(p), sources.selectionFor(s),
			sources.forAgent(s.Agent).tables)
		return s, layer, err
	}
	return manifest.Surface{}, nil, fmt.Errorf("pack %s declares no config surface at %s", p.Name, want)
}

// HostModelList is yolo's list for the model menu menu declares on a program of p, as p's derive
// composes it over in, the launch's inputs (in.Vars its three wire tables, in.Packs its selected
// packs): nil when the derive names no model for the launch's provider, which is "no menu this
// launch". home is the real home the launch runs in; nothing is written into it. A derive's
// warnings are handed to warn, one line each.
//
// An error means the list could not be composed at all: p declares no surface at the path the
// declaration reads, its surfaces do not decode, or its derive failed. The caller warns and the
// program keeps its own menu, as for every other failure of the step.
func HostModelList(p *packload.Pack, menu packdecl.ModelMenu, home string, in *HostInputs,
	warn func(string)) ([]modelmenu.ListEntry, error) {
	// The path equality a jail relies on too: its boot writes the surface there, and its
	// launcher reads the declaration's `list` from there (TestCodexModelMenuReadsTheListItsSurfaceWrites).
	s, layer, err := hostSurfaceLayer(p, menu.List, home, in, warn)
	if err != nil {
		if s.Path == "" {
			return nil, fmt.Errorf("%v, where its model_menu reads the list", err)
		}
		return nil, err
	}
	data, err := json.Marshal(layer)
	if err != nil {
		return nil, fmt.Errorf("surface %s/%s: its derive's output is not JSON: %v", s.Agent, s.Name, err)
	}
	return modelmenu.ParseList(data), nil
}

// LaunchSelection is one host launch's LAUNCH SELECTION (packdecl.LaunchSelection) for one program:
// what the pack's derives compose for it over one launch's tables, ready to compare with another
// launch's and to hand to the program. The zero Selection is "the derive composed none", which
// hands nothing.
type LaunchSelection struct {
	// Spec is the pack's declaration.
	Spec packdecl.LaunchSelection
	// Selection is the reserved `selection` namespace the derive returned for Spec.Surface
	// (agentcfg.SelectionKey), nil when it returned none. Spec.Defaults are not in it.
	Selection map[string]any
	// Rows are the rows of Spec.Rows.Table the selection names, by key; nil when none.
	Rows map[string]any
	// Surfaces is each Spec.Surfaces path's content as this launch's derive composes it, encoded
	// in that surface's codec.
	Surfaces map[string]string
	// codec is Spec.Surface's codec name, which the each and env forms encode values in.
	codec string
}

// HostLaunchSelection composes p's launch selection, spec, over in, the launch's inputs: the
// selection p's derive returns for spec.Surface, the rows it names, and each further surface's
// content. home is the real home the launch runs in; nothing is written into it. A derive's
// warnings are handed to warn; nil discards them, for a composition that is only compared.
//
// An error means it could not be composed at all: a surface the declaration names is not one of
// p's, does not decode, is not a computed surface where one must be, or its derive failed.
func HostLaunchSelection(p *packload.Pack, spec packdecl.LaunchSelection, home string, in *HostInputs,
	warn func(string)) (*LaunchSelection, error) {
	s, layer, err := hostSurfaceLayer(p, spec.Surface, home, in, warn)
	if err != nil {
		return nil, err
	}
	out := &LaunchSelection{Spec: spec, codec: s.Codec}
	_, sel, problems := agentcfg.TakeSelection(layer)
	if len(problems) > 0 {
		return nil, fmt.Errorf("surface %s/%s: %s", s.Agent, s.Name, problems[0])
	}
	if len(sel) > 0 {
		out.Selection = sel
	}
	if spec.Rows != nil && out.Selection != nil {
		table, _ := layer[spec.Rows.Table].(map[string]any)
		handed := out.handed()
		for _, key := range spec.Rows.NamedBy {
			for _, name := range selectionNames(handed[key]) {
				if row, ok := table[name]; ok {
					if out.Rows == nil {
						out.Rows = map[string]any{}
					}
					out.Rows[name] = row
				}
			}
		}
	}
	for _, rel := range sortedStrings(spec.Surfaces) {
		fs, flayer, err := hostSurfaceLayer(p, rel, home, in, warn)
		if err != nil {
			return nil, err
		}
		if fs.ResolvedMode() != manifest.ModeComputed {
			return nil, fmt.Errorf("surface %s/%s at %s is %s, not computed, so its content is not its "+
				"derive's alone and cannot be handed to one launch", fs.Agent, fs.Name, fs.Path, fs.ResolvedMode())
		}
		if flayer == nil {
			flayer = map[string]any{}
		}
		text, err := encodeDocument(fs.Codec, flayer)
		if err != nil {
			return nil, fmt.Errorf("surface %s/%s: %v", fs.Agent, fs.Name, err)
		}
		if out.Surfaces == nil {
			out.Surfaces = map[string]string{}
		}
		out.Surfaces[rel] = text
	}
	return out, nil
}

// sortedStrings is m's keys, sorted.
func sortedStrings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// selectionNames is the row names one selection value spells: a string, or an array's strings.
func selectionNames(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Empty reports whether there is nothing to hand: no selection, so the program starts on its file.
func (l *LaunchSelection) Empty() bool { return l == nil || len(l.Selection) == 0 }

// Same reports whether l and o would hand the program the same thing: the same selection, rows and
// surfaces. Two empty selections are the same whatever their surfaces say, since an empty one hands
// nothing.
func (l *LaunchSelection) Same(o *LaunchSelection) bool {
	if l.Empty() || o.Empty() {
		return l.Empty() == o.Empty()
	}
	return reflect.DeepEqual(canonical(l.Selection), canonical(o.Selection)) &&
		reflect.DeepEqual(canonical(l.Rows), canonical(o.Rows)) && reflect.DeepEqual(l.Surfaces, o.Surfaces)
}

// canonical is v through JSON, so two layers with one meaning compare equal whatever Go types the
// derive engine chose for their numbers.
func canonical(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

// handed is the selection the program is handed: the derive's, with Spec.Defaults filling every
// key it omits.
func (l *LaunchSelection) handed() map[string]any {
	out := map[string]any{}
	for k, v := range l.Selection {
		out[k] = v
	}
	for k, v := range l.Spec.Defaults {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out
}

// Keys names the selection l hands, for a launch's disclosure: each selection key with its value,
// defaults included, then each row by name ("the model_providers row for zai"). The surfaces are
// named by the variables that carry them (Var.Carries).
func (l *LaunchSelection) Keys() []string {
	if l.Empty() {
		return nil
	}
	sel := l.handed()
	var out []string
	for _, k := range sortedKeys(sel) {
		out = append(out, k+"="+plainWord(sel[k]))
	}
	for _, name := range sortedKeys(l.Rows) {
		out = append(out, "the "+l.Spec.Rows.Table+" row for "+name)
	}
	return out
}

// Argv is the words the each or flags form hands, in order, to go right after the program's
// argv[0]; nil for the env form or an empty selection. An error names a value the form cannot
// spell, and then nothing is handed.
func (l *LaunchSelection) Argv() ([]string, error) {
	if l.Empty() {
		return nil, nil
	}
	sel := l.handed()
	switch {
	case len(l.Spec.Each) > 0:
		doc := map[string]any{}
		for k, v := range sel {
			doc[k] = v
		}
		if len(l.Rows) > 0 {
			doc[l.Spec.Rows.Table] = l.Rows
		}
		var words []string
		for _, leaf := range leaves(nil, doc) {
			for _, seg := range leaf.path {
				if strings.Contains(seg, ".") {
					return nil, fmt.Errorf("the key %q of %s holds a \".\", which a dotted {key} cannot spell",
						seg, strings.Join(leaf.path, "."))
				}
			}
			value, err := encodeValue(l.codec, leaf.value)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", strings.Join(leaf.path, "."), err)
			}
			key := strings.Join(leaf.path, ".")
			for _, w := range l.Spec.Each {
				w = strings.ReplaceAll(w, packdecl.LaunchSelectionKey, key)
				words = append(words, strings.ReplaceAll(w, packdecl.LaunchSelectionValue, value))
			}
		}
		return words, nil
	case len(l.Spec.Flags) > 0:
		var words []string
		for _, f := range l.Spec.Flags {
			v, ok := sel[f.Key]
			if !ok {
				continue
			}
			requirementsMet := true
			for _, required := range f.Requires {
				if _, present := sel[required]; !present {
					requirementsMet = false
					break
				}
			}
			if !requirementsMet {
				continue
			}
			value, err := flagValue(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", f.Key, err)
			}
			for _, w := range f.Argv {
				words = append(words, strings.ReplaceAll(w, packdecl.LaunchSelectionValue, value))
			}
		}
		return words, nil
	}
	return nil, nil
}

// Var is one variable a launch selection sets in the program's environment.
type Var struct {
	Name, Value string
	// Carries is what the variable holds, in words, for the disclosure: the selection's keys for
	// the env form's document, a surface's path for a surface.
	Carries string
	// Merged is set when Value is the user's own value of Name with the selection merged over it.
	Merged bool
}

// Vars is the variables l sets: the env form's document, then each surface's content, in path
// order; nil for an empty selection. lookup is the program's environment as composed so far: a
// value it already holds for the env form's variable is decoded in the surface's codec and the
// selection merged over it, the selection's keys winning and every other key of the user's kept,
// so one launch's -p never discards what the user set there. An error names a value that does not
// decode as a document, and then nothing is handed.
func (l *LaunchSelection) Vars(lookup func(string) (string, bool)) ([]Var, error) {
	if l.Empty() {
		return nil, nil
	}
	var out []Var
	if name := l.Spec.Env; name != "" {
		doc := l.handed()
		if len(l.Rows) > 0 {
			doc[l.Spec.Rows.Table] = l.Rows
		}
		merged := false
		if have, ok := lookup(name); ok && strings.TrimSpace(have) != "" {
			c, found := codec.LookupCodec(l.codec)
			if !found {
				return nil, fmt.Errorf("no codec %q to read your own %s with", l.codec, name)
			}
			userDoc, err := c.Decode([]byte(have))
			user, isMap := userDoc.(map[string]any)
			if err != nil || !isMap {
				return nil, fmt.Errorf("your own %s is not a %s object, so the selection cannot be merged into it",
					name, l.codec)
			}
			doc = mergeOver(user, doc)
			merged = true
		}
		text, err := encodeDocument(l.codec, doc)
		if err != nil {
			return nil, err
		}
		out = append(out, Var{Name: name, Value: text, Carries: strings.Join(l.Keys(), ", "), Merged: merged})
	}
	for _, rel := range sortedStrings(l.Spec.Surfaces) {
		if text, ok := l.Surfaces[rel]; ok {
			out = append(out, Var{Name: l.Spec.Surfaces[rel], Value: text,
				Carries: "~/" + rel + " as this launch composes it, read before the file"})
		}
	}
	return out, nil
}

// mergeOver is over merged onto base: objects merged key by key, every other value of over
// replacing base's. Neither input is changed.
func mergeOver(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = mergeOver(bm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// leaf is one non-object value of a document and its key path.
type leaf struct {
	path  []string
	value any
}

// leaves is every non-null, non-object value under doc, by sorted key path. A null is skipped: no
// per-leaf override can spell "remove this key".
func leaves(prefix []string, doc map[string]any) []leaf {
	var out []leaf
	for _, k := range sortedKeys(doc) {
		path := append(append([]string(nil), prefix...), k)
		switch v := doc[k].(type) {
		case nil:
		case map[string]any:
			out = append(out, leaves(path, v)...)
		default:
			out = append(out, leaf{path: path, value: v})
		}
	}
	return out
}

// encodeValue is one value in codecName's spelling: a TOML literal, or JSON.
func encodeValue(codecName string, v any) (string, error) {
	switch codecName {
	case "toml":
		text, err := (codec.TOML{}).Encode(map[string]any{"v": v})
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(strings.TrimPrefix(string(text), "v = "), "\n"), nil
	case "json":
		return compactJSON(v)
	}
	return "", fmt.Errorf("a value cannot be spelled in codec %q", codecName)
}

// encodeDocument is doc in codecName's spelling, compact for JSON, which a variable carries on one
// line.
func encodeDocument(codecName string, doc map[string]any) (string, error) {
	if codecName == "json" {
		return compactJSON(doc)
	}
	c, ok := codec.LookupCodec(codecName)
	if !ok {
		return "", fmt.Errorf("no codec %q", codecName)
	}
	text, err := c.Encode(doc)
	return string(text), err
}

func compactJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// flagValue is one selection value as a plain word: a string as it is, a number or a boolean as
// its literal, an array's items joined by commas. An item holding a comma, or an object, cannot be
// spelled.
func flagValue(v any) (string, error) {
	if items, ok := v.([]any); ok {
		parts := make([]string, 0, len(items))
		for _, item := range items {
			if _, nested := item.([]any); nested {
				return "", fmt.Errorf("an array inside an array cannot be spelled as one word")
			}
			w, err := flagValue(item)
			if err != nil {
				return "", err
			}
			if strings.Contains(w, ",") {
				return "", fmt.Errorf("the item %q holds a comma, which joins the items", w)
			}
			parts = append(parts, w)
		}
		return strings.Join(parts, ","), nil
	}
	if _, isMap := v.(map[string]any); isMap {
		return "", fmt.Errorf("an object cannot be spelled as one word")
	}
	return plainWord(v), nil
}

// plainWord is a scalar as a plain word, an array's items joined by commas.
func plainWord(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, plainWord(e))
		}
		return strings.Join(parts, ",")
	}
	text, _ := compactJSON(v)
	return text
}

// warnWriter is an io.Writer over a line callback: each line written is one call, without its
// newline. A nil callback discards.
type warnWriter func(string)

func (w warnWriter) Write(b []byte) (int, error) {
	if w == nil {
		return len(b), nil
	}
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line != "" {
			w(line)
		}
	}
	return len(b), nil
}
