package cli

// configrefexamples_test.go runs every config example `yolo config-ref` prints through
// the code that reads config, so the reference cannot drift from the readers.
//
// The reference is TOML first, and it keeps a LEGACY JSONC FORMAT section for as long as
// yolo reads the old format (the owner's ruling of 2026-11-07: "keep a legacy jsonc
// reference in there until we delete it entirely and keep it up to date, along with making
// the primary toml now"). So:
//
//   - every TOML example loads through the real loader and validates with no error;
//   - every JSONC example sits in the legacy section, and either is "Legacy only" and loads
//     through the legacy reader, or follows its TOML twin and converts to it: the migration
//     engine proves the conversion equal, and the converted file and the printed twin load
//     to the same effective config;
//   - every key either reader accepts is documented, and no legacy `"key":` spelling is
//     left outside the legacy section.
//
// An EXAMPLE BLOCK (a name coined here) is a header line ending in
// `Example (<file>):` or `Legacy only (<file>):`, followed by the lines indented deeper
// than that header. The file names the scope and the format the block is read in.

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

type refExample struct {
	line   int    // 1-based line of the header in config_ref.txt
	kind   string // "Example" or "Legacy only"
	file   string // as printed: yolo-jail.toml, ~/.config/yolo-jail/config.jsonc, ...
	body   string // the block, dedented
	legacy bool   // inside the LEGACY JSONC FORMAT section
}

func (e refExample) isTOML() bool { return strings.HasSuffix(e.file, ".toml") }

var refExampleHeader = regexp.MustCompile(`(Example|Legacy only) \((yolo-jail\.toml|yolo-jail\.local\.toml|~/\.config/yolo-jail/config\.toml|yolo-jail\.jsonc|yolo-jail\.local\.jsonc|~/\.config/yolo-jail/config\.jsonc)\):$`)

func leadingSpaces(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

// legacySectionBounds is the [start, end) line range of the LEGACY JSONC FORMAT section.
func legacySectionBounds(t *testing.T, lines []string) (int, int) {
	t.Helper()
	start := -1
	for i, l := range lines {
		if start < 0 && l == "[bold cyan]LEGACY JSONC FORMAT[/bold cyan]" {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(l, "[bold cyan]") {
			return start, i
		}
	}
	if start < 0 {
		t.Fatal("config-ref has no LEGACY JSONC FORMAT section")
	}
	return start, len(lines)
}

// legacySection is the text of the LEGACY JSONC FORMAT section.
func legacySection(t *testing.T) string {
	t.Helper()
	lines := strings.Split(configRefText(t), "\n")
	start, end := legacySectionBounds(t, lines)
	return strings.Join(lines[start:end], "\n")
}

func configRefExamples(t *testing.T) []refExample {
	t.Helper()
	lines := strings.Split(configRefText(t), "\n")
	lstart, lend := legacySectionBounds(t, lines)
	var out []refExample
	for i, l := range lines {
		m := refExampleHeader.FindStringSubmatch(strings.TrimRight(l, " "))
		if m == nil {
			if strings.Contains(l, "Example (") || strings.Contains(l, "Legacy only (") {
				t.Errorf("config_ref.txt:%d: %q names a file this test does not read; spell the header "+
					"`Example (<file>):` with a config file name, so its block is checked", i+1, l)
			}
			continue
		}
		indent := leadingSpaces(l)
		var block []string
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			if strings.TrimSpace(next) == "" {
				// A blank line belongs to the block only when the block continues after it.
				k := j + 1
				for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
					k++
				}
				if k < len(lines) && leadingSpaces(lines[k]) > indent {
					block = append(block, "")
					continue
				}
				break
			}
			if leadingSpaces(next) <= indent {
				break
			}
			block = append(block, next)
		}
		if len(block) == 0 {
			t.Errorf("config_ref.txt:%d: %q is followed by no indented example", i+1, l)
			continue
		}
		cut := -1
		for _, b := range block {
			if b == "" {
				continue
			}
			if n := leadingSpaces(b); cut < 0 || n < cut {
				cut = n
			}
		}
		for k, b := range block {
			if len(b) >= cut {
				block[k] = b[cut:]
			}
		}
		out = append(out, refExample{
			line: i + 1, kind: m[1], file: m[2], body: strings.Join(block, "\n") + "\n",
			legacy: i > lstart && i < lend,
		})
	}
	return out
}

// loadRefConfig writes body as file into a fresh home and workspace, loads the effective
// config the way a launch does, and validates it.
func loadRefConfig(t *testing.T, file, body string) (*jsonx.OrderedMap, []string, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	path := filepath.Join(ws, file)
	if rest, ok := strings.CutPrefix(file, "~/.config/yolo-jail/"); ok {
		path = filepath.Join(userDir, rest)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadConfigWithSources(ws, true, func(string) {})
	if err != nil {
		return nil, nil, err
	}
	all, _ := config.ValidateConfig(cfg, ws, nil)
	var errs []string
	for _, e := range all {
		if !isHostStateError(e) {
			errs = append(errs, strings.ReplaceAll(e, ws, "<workspace>"))
		}
	}
	// Paths the loader resolved against this temp workspace (env.sources, mounts) would make
	// two loads of the same text differ, so they are written relative to a fixed name.
	return relocate(cfg, ws), errs, nil
}

// hostStateErrors are validator findings about the machine the example is read on, not
// about its spelling: a path that does not exist here, a program no selected pack installs,
// or a request the example's other half (the user config) allows. Any finding about a key
// or a value's shape still fails the test.
var hostStateErrors = []string{
	"does not exist",
	"no selected pack declares",
	"your user config does not let a project ask for one",
}

func isHostStateError(e string) bool {
	for _, frag := range hostStateErrors {
		if strings.Contains(e, frag) {
			return true
		}
	}
	return false
}

func relocate(v any, ws string) *jsonx.OrderedMap {
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case *jsonx.OrderedMap:
			out := jsonx.NewOrderedMap()
			for _, k := range x.Keys() {
				val, _ := x.Get(k)
				out.Set(k, walk(val))
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = walk(e)
			}
			return out
		case string:
			return strings.ReplaceAll(x, ws, "<workspace>")
		default:
			return v
		}
	}
	return walk(v).(*jsonx.OrderedMap)
}

func TestConfigRefExamplesLoad(t *testing.T) {
	var toml, legacyOnly int
	for _, ex := range configRefExamples(t) {
		if !ex.isTOML() && ex.kind != "Legacy only" {
			continue // a twin; TestConfigRefLegacyExamplesConvertToTheirTOMLTwin reads it
		}
		if ex.isTOML() {
			if ex.kind != "Example" {
				t.Errorf("config_ref.txt:%d: a TOML block is an Example, not %q", ex.line, ex.kind)
			}
			toml++
		} else {
			if !ex.legacy {
				t.Errorf("config_ref.txt:%d: a JSONC example outside LEGACY JSONC FORMAT", ex.line)
			}
			legacyOnly++
		}
		_, errs, err := loadRefConfig(t, ex.file, ex.body)
		if err != nil {
			t.Errorf("config_ref.txt:%d: the %s example does not load: %v\n%s", ex.line, ex.file, err, ex.body)
			continue
		}
		if len(errs) > 0 {
			t.Errorf("config_ref.txt:%d: the %s example does not validate: %v\n%s", ex.line, ex.file, errs, ex.body)
		}
	}
	// A floor, so a header format change that stops this test finding blocks fails here.
	if toml < 60 {
		t.Errorf("found only %d TOML examples in config-ref; the header format changed or examples were lost", toml)
	}
	if legacyOnly < 3 {
		t.Errorf("found only %d legacy-only examples in config-ref", legacyOnly)
	}
}

// twinScopeMatches: a JSONC example's TOML twin is the same scope's TOML file.
func twinScopeMatches(jsonc, toml string) bool {
	return strings.TrimSuffix(jsonc, ".jsonc") == strings.TrimSuffix(toml, ".toml")
}

func TestConfigRefLegacyExamplesConvertToTheirTOMLTwin(t *testing.T) {
	examples := configRefExamples(t)
	pairs := 0
	for i, ex := range examples {
		if ex.isTOML() || ex.kind != "Example" {
			continue
		}
		if !ex.legacy {
			t.Errorf("config_ref.txt:%d: a JSONC example outside LEGACY JSONC FORMAT", ex.line)
		}
		if i == 0 || !examples[i-1].isTOML() || !twinScopeMatches(ex.file, examples[i-1].file) {
			t.Errorf("config_ref.txt:%d: a JSONC Example must directly follow its TOML twin of the same "+
				"scope (or be headed `Legacy only` when TOML cannot say it)", ex.line)
			continue
		}
		twin := examples[i-1]
		pairs++

		converted := migrateRefExample(t, ex)
		if converted == "" {
			continue
		}
		want := effectiveJSON(t, twin, twin.body)
		got := effectiveJSON(t, twin, converted)
		if want != got {
			t.Errorf("config_ref.txt:%d: the legacy example converts to a config that says something "+
				"other than its TOML twin at line %d.\n--- migration wrote ---\n%s--- twin loads as ---\n%s\n"+
				"--- migration loads as ---\n%s", ex.line, twin.line, converted, want, got)
		}
	}
	if pairs < 7 {
		t.Errorf("found only %d legacy/TOML example pairs in config-ref", pairs)
	}
}

// migrateRefExample runs `yolo config migrate`'s engine on one legacy example and returns
// the TOML it writes, failing when the engine cannot prove the conversion equal.
func migrateRefExample(t *testing.T, ex refExample) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	req := configmigrate.Request{}
	if rest, ok := strings.CutPrefix(ex.file, "~/.config/yolo-jail/"); ok {
		if err := os.WriteFile(filepath.Join(userDir, rest), []byte(ex.body), 0o644); err != nil {
			t.Fatal(err)
		}
		req.UserConfigDir = userDir
	} else {
		if err := os.WriteFile(filepath.Join(ws, ex.file), []byte(ex.body), 0o644); err != nil {
			t.Fatal(err)
		}
		req.Workspace = ws
	}
	g, err := configmigrate.Discover(req)
	if err != nil {
		t.Errorf("config_ref.txt:%d: migration cannot read the legacy example: %v", ex.line, err)
		return ""
	}
	p, err := configmigrate.Plan(g)
	if err != nil {
		t.Errorf("config_ref.txt:%d: migration refuses the legacy example: %v", ex.line, err)
		return ""
	}
	if !p.Equal || len(p.NotConverted) > 0 || p.Refused != nil {
		t.Errorf("config_ref.txt:%d: migration does not convert the legacy example whole: equal=%v "+
			"not converted=%v refused=%v", ex.line, p.Equal, p.NotConverted, p.Refused)
		return ""
	}
	if len(p.Targets) != 1 {
		t.Errorf("config_ref.txt:%d: migration wrote %d files for one example", ex.line, len(p.Targets))
		return ""
	}
	return string(p.Targets[0].Bytes)
}

// effectiveJSON loads body as the twin's TOML file and returns the effective config as
// canonical JSON.
func effectiveJSON(t *testing.T, twin refExample, body string) string {
	t.Helper()
	cfg, errs, err := loadRefConfig(t, twin.file, body)
	if err != nil {
		t.Fatalf("config_ref.txt:%d: %v\n%s", twin.line, err, body)
	}
	if len(errs) > 0 {
		t.Errorf("config_ref.txt:%d: %v\n%s", twin.line, errs, body)
	}
	// A migration leaves out a value that equals its default (configmigrate's dropDefaults),
	// so both sides are compared with every default filled in, as the migration's own
	// equality check does.
	config.ApplyWrittenDefaults(cfg)
	return string(canonicalJSON(t, cfg))
}

func canonicalJSON(t *testing.T, cfg *jsonx.OrderedMap) []byte {
	t.Helper()
	var buf bytes.Buffer
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case *jsonx.OrderedMap:
			keys := append([]string(nil), x.Keys()...)
			sort.Strings(keys)
			buf.WriteString("{")
			for i, k := range keys {
				if i > 0 {
					buf.WriteString(",")
				}
				buf.WriteString(jsonx.MustDumpsCompact(k))
				buf.WriteString(":")
				val, _ := x.Get(k)
				walk(val)
			}
			buf.WriteString("}")
		case []any:
			buf.WriteString("[")
			for i, e := range x {
				if i > 0 {
					buf.WriteString(",")
				}
				walk(e)
			}
			buf.WriteString("]")
		default:
			buf.WriteString(jsonx.MustDumpsCompact(x))
		}
	}
	walk(cfg)
	return buf.Bytes()
}

// Every top-level key the TOML reader accepts has a section under its own name or a
// dotted sub-key's, and every legacy key whose spelling changed is named in the legacy
// section.
func TestConfigRefDocumentsEveryKeyInBothFormats(t *testing.T) {
	titles := configRefSectionTitles(t)
	documented := func(root string) bool {
		for _, title := range titles {
			if title == root || strings.HasPrefix(title, root+".") {
				return true
			}
		}
		return false
	}
	roots := map[string]bool{}
	for _, k := range config.ReadableSchemaKeys() {
		roots[strings.SplitN(k.Path, ".", 2)[0]] = true
	}
	toml := tomlSection(t)
	for root := range roots {
		if !documented(root) && !strings.Contains(toml, "[bold]"+root+"[/bold]") {
			t.Errorf("the TOML key %q has no section in config-ref, and THE TOML FORMAT does not name it", root)
		}
	}
	legacy := legacySection(t)
	live := map[string]bool{}
	for _, k := range config.TopLevelConfigKeys() {
		live[k] = true
	}
	for _, d := range config.RenameDescriptors() {
		if strings.Contains(d.OldPath, "*") || len(d.Replacements) == 0 {
			continue
		}
		if len(d.Replacements) == 1 && d.Replacements[0] == d.OldPath {
			continue // the same spelling in both formats, such as network.mode
		}
		root := strings.SplitN(d.OldPath, ".", 2)[0]
		if !live[root] {
			continue // retired: TestConfigRefAnnouncesTheRefusalForEveryRetiredKey reads those
		}
		if !strings.Contains(legacy, d.OldPath) {
			t.Errorf("legacy key %q is spelled %v in TOML, but LEGACY JSONC FORMAT does not name it", d.OldPath, d.Replacements)
		}
	}
}

// No legacy JSON spelling of a key is left outside the legacy section: a `"key":` there
// teaches a spelling the TOML reader refuses.
func TestConfigRefPrintsNoLegacySpellingOutsideTheLegacySection(t *testing.T) {
	lines := strings.Split(configRefText(t), "\n")
	start, end := legacySectionBounds(t, lines)
	for i, l := range lines {
		if i >= start && i < end {
			continue
		}
		for _, key := range config.TopLevelConfigKeys() {
			if strings.Contains(l, `"`+key+`":`) {
				t.Errorf("config_ref.txt:%d: the legacy spelling %q appears outside LEGACY JSONC FORMAT: %s", i+1, `"`+key+`":`, l)
			}
		}
	}
}
