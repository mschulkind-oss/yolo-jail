package config

// launchorigins.go carries the provenance (sources.go) of a config a launch hands a jail ACROSS
// the boundary, so a refusal inside the jail names the host file that set the key, not the
// generated copy the jail reads.
//
// THE PROBLEM IT SOLVES. A jail reads two configs the host composed for it: the generated
// user-scope files (inherit.go: ~/.config/yolo-jail/config.jsonc and inherited-launch.jsonc) and
// the assembled config (assembled.go), which LoadConfig reads back for the jail's own workspace.
// Neither is a file the user wrote. Before this, a refusal about a key in the first named a line
// of the generated file, and one in the second named no file at all — so a user told
// `config.providers.x.models.y.wireId: unknown key` searched every config file they own, and an
// edit to the right one changed nothing until a relaunch, which nothing said.
//
// HOW. The host writes, beside each copy, its LAUNCH ORIGINS: for every value, the labels of the
// files that wrote it ("~/.config/yolo-jail/config.jsonc"), lowest precedence first, recorded at
// a value only where they differ from its parent's — the per-file record without its line
// numbers, which name lines of a host file the jail cannot read anyway. In the generated files
// the record is one JSONC comment line (originsLinePrefix), so the file is still the config it
// was; beside the assembled copy it is a file of its own (WorkspaceAssembledOriginsPath). A jail
// that reads either builds its provenance tree from the record, each origin keeping the copy's
// own file and adding the host label, and Annotate then says where the key was set and that the
// jail reads a copy made at launch.
//
// THE RECORD IS ONLY EVER WORDS. It is read only in a jail, only at the paths the host generates
// (the user-scope files by path, the assembled copy's record only while its digest matches the
// copy), and it changes what a message says and nothing a gate decides: an origin with a host
// label still carries the copy's own file, so every reader that asks which file wrote a value
// (gateread.go, brokered.go) gets the answer it got before. The one thing besides words it
// settles is the FORMAT a value was written in (srcOrigin.format, read off the label's
// extension), which picks the schema the jail judges the value by — readable for a TOML host
// writer, legacy otherwise — exactly as the host judged it; the copy's own format says nothing
// about that, the assembled copy being JSON whatever wrote it. The jail can write some of those
// paths, so a forged record can only mislabel a message or have the jail judge its own copy
// by the other schema, which it could do by editing the copy, and every label is escaped on
// the way in. It does show the jail the NAMES of host files that set its keys, includes among them,
// never their contents.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// originsLinePrefix starts the comment line of a generated user-scope file that carries its
// launch origins (inherit.go's RenderFilteredWithOrigins). It sits in the file's leading comment
// block, which is where a jail looks for it.
const originsLinePrefix = "// yolo-origins: "
const originsTOMLLinePrefix = "# yolo-origins: "

// originTree is the launch-origins record of one value: W is the labels of the files that wrote
// it, lowest precedence first, absent where they are its parent's; K and E are its members and
// elements, absent where every one of them is its parent's too.
type originTree struct {
	W []string               `json:"w,omitempty"`
	K map[string]*originTree `json:"k,omitempty"`
	E []*originTree          `json:"e,omitempty"`
}

// writerLabels is the label of every file that wrote n, lowest precedence first, a launch copy's
// origin by its host label, each run of one label once.
func (n *srcNode) writerLabels() []string {
	var out []string
	for _, w := range n.writerList() {
		label := w.host
		if label == "" && w.file != nil {
			label = w.file.label()
		}
		if label != "" && (len(out) == 0 || out[len(out)-1] != label) {
			out = append(out, label)
		}
	}
	return out
}

// originsOf is the launch-origins record of v, whose provenance is n, nil when every value in it
// was written where its parent's was (or nothing is known about it).
func originsOf(n *srcNode, v any, parent []string) *originTree {
	if n == nil {
		return nil
	}
	labels := n.writerLabels()
	if len(labels) == 0 {
		labels = parent
	}
	t := &originTree{}
	if !slices.Equal(labels, parent) {
		t.W = labels
	}
	switch x := v.(type) {
	case *jsonx.OrderedMap:
		for _, k := range x.Keys() {
			cv, _ := x.Get(k)
			if c := originsOf(n.key(k), cv, labels); c != nil {
				if t.K == nil {
					t.K = map[string]*originTree{}
				}
				t.K[k] = c
			}
		}
	case []any:
		elems := make([]*originTree, len(x))
		recorded := false
		for i, e := range x {
			elems[i] = originsOf(n.elem(i), e, labels)
			recorded = recorded || elems[i] != nil
		}
		if recorded {
			t.E = elems
		}
	}
	if t.W == nil && t.K == nil && t.E == nil {
		return nil
	}
	return t
}

// LaunchOriginsJSON is the launch-origins record of cfg, whose provenance is src, as one line of
// JSON; "" when src records nothing about cfg.
func LaunchOriginsJSON(cfg *jsonx.OrderedMap, src *Sources) string {
	if cfg == nil {
		return ""
	}
	t := originsOf(src.node(), cfg, nil)
	if t == nil {
		return ""
	}
	b, err := json.Marshal(t)
	if err != nil {
		return ""
	}
	return string(b)
}

// launchCopyTree is the provenance tree of v, a value of the launch copy f, built from its
// launch-origins record t: every value keeps f and its place in f, and adds the host label of
// each file the record says wrote it. A value the record says nothing about takes its parent's.
func launchCopyTree(f *srcFile, v any, t *originTree) *srcNode {
	return launchCopyTreeAt(f, v, nil, nil, t, nil)
}

func launchCopyTreeAt(f *srcFile, v any, steps []json5.Step, documentSteps []configdoc.Step, t *originTree, parent []string) *srcNode {
	labels := parent
	if t != nil && len(t.W) > 0 {
		labels = t.W
	}
	n := &srcNode{}
	for _, l := range labels {
		// Escaped again on the way in: the assembled copy's record sits in the workspace, which
		// the jail can write, and every located message leads with or ends in this label.
		n.writers = append(n.writers, srcOrigin{file: f, steps: steps, documentSteps: documentSteps, host: termsafe.Visible(l)})
	}
	if len(n.writers) == 0 {
		n.writers = []srcOrigin{{file: f, steps: steps, documentSteps: documentSteps}}
	}
	switch x := v.(type) {
	case *jsonx.OrderedMap:
		n.keys = make(map[string]*srcNode, x.Len())
		for _, k := range x.Keys() {
			cv, _ := x.Get(k)
			var ct *originTree
			if t != nil {
				ct = t.K[k]
			}
			n.keys[k] = launchCopyTreeAt(f, cv, appendStep(steps, json5.Key(k)), appendDocumentStep(documentSteps, configdoc.Step{Key: k}), ct, labels)
		}
	case []any:
		n.elems = make([]*srcNode, len(x))
		for i, e := range x {
			var ct *originTree
			if t != nil && i < len(t.E) {
				ct = t.E[i]
			}
			n.elems[i] = launchCopyTreeAt(f, e, appendStep(steps, json5.Elem(i)), appendDocumentStep(documentSteps, configdoc.Step{Index: i, IsIndex: true}), ct, labels)
		}
	}
	return n
}

// parseOrigins decodes a launch-origins record, nil when it does not decode.
func parseOrigins(data []byte) *originTree {
	var t originTree
	if len(bytes.TrimSpace(data)) == 0 || json.Unmarshal(data, &t) != nil {
		return nil
	}
	return &t
}

// generatedOrigins is the launch-origins record a generated user-scope file carries in its
// leading comment block, nil when it carries none.
func generatedOrigins(data []byte) *originTree {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//") {
			if rest, ok := strings.CutPrefix(line, originsLinePrefix); ok {
				return parseOrigins([]byte(rest))
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			if rest, ok := strings.CutPrefix(line, originsTOMLLinePrefix); ok {
				return parseOrigins([]byte(rest))
			}
			continue
		}
		return nil
	}
	return nil
}

// isGeneratedUserScope reports whether path is one of the user-scope files a launch generated
// for this jail (inherit.go), the only files whose launch-origins line a reader believes.
func isGeneratedUserScope(path string) bool {
	if !inJail() || path == "" {
		return false
	}
	userDir := filepath.Dir(paths.UserConfigPath())
	return path == paths.UserConfigPath() ||
		path == filepath.Join(userDir, paths.UserConfigTOMLName) ||
		path == filepath.Join(userDir, "inherited-launch.jsonc") ||
		path == filepath.Join(userDir, "inherited-launch.toml")
}

// launchCopyStep is the clause a refusal located through a launch copy ends with: the next step
// that makes an edit to the host file count.
const launchCopyStep = "; this jail reads a copy made at launch, so an edit there takes effect when the jail is relaunched)"

// launchCopyNote is what a refusal adds when the key it names was set in a host file the jail
// reads a launch copy of: the host files that set it, highest precedence first, then any file
// of the jail's own that writes it too, then the next step — which is about the host files alone.
func launchCopyNote(origins []srcOrigin) string {
	var host, live []string
	for _, o := range origins {
		if o.host != "" {
			host = append(host, o.String())
		} else {
			live = append(live, o.String())
		}
	}
	note := " (set in " + strings.Join(host, ", ")
	if len(live) > 0 {
		note += ", also written at " + strings.Join(live, ", ")
	}
	return note + launchCopyStep
}

// LocatedInLaunchCopy reports whether msg is a refusal Annotate located through a launch copy:
// one about a key set in a host file, whose fix is an edit there and a relaunch, so a caller
// naming the next step names that one rather than a file in the jail.
func LocatedInLaunchCopy(msg string) bool {
	return strings.HasSuffix(msg, launchCopyStep)
}
