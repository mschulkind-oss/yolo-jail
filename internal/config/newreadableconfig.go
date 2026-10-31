package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const readableConfigFormat = 2

// ConfigReadInput substitutes planned bytes for one explicitly named physical file during a single
// standard config read. The standard reader must still open the path successfully and applies
// its normal scope checks to these bytes. Construct with NewConfigReadInput; the target must be an
// absolute physical path, not a provenance label or an inferred source.
type ConfigReadInput struct {
	target      string
	bytes       []byte
	witness     *editReadWitness
	roleAudit   *[]editTargetReadObservation
	prospective *prospectiveRead
}

// NewConfigReadInput makes an immutable invocation input. Both the physical path and bytes are copied
// into private storage so later caller mutation cannot affect a read.
func NewConfigReadInput(physicalTarget string, data []byte) (ConfigReadInput, error) {
	if !filepath.IsAbs(physicalTarget) {
		return ConfigReadInput{}, fmt.Errorf("read input target must be an absolute physical path: %q", physicalTarget)
	}
	return ConfigReadInput{target: filepath.Clean(physicalTarget), bytes: append([]byte(nil), data...)}, nil
}

func (input ConfigReadInput) clone() *ConfigReadInput {
	return &ConfigReadInput{
		target: input.target, bytes: append([]byte(nil), input.bytes...), witness: input.witness,
		roleAudit: input.roleAudit, prospective: cloneProspectiveRead(input.prospective),
	}
}

// loadConfigFile dispatches by the selected filename. Legacy JSONC/JSON reads stay on their
// existing decoder and error path; TOML goes through the shared positioned document contract.
func loadConfigFile(path, label string, strict bool, warn Warn, record bool, scope SchemaScope, at readAt) (*jsonx.OrderedMap, *srcFile, *srcNode, error) {
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		return loadTOMLConfigFile(path, label, strict, warn, record, scope, at)
	}
	return loadJSONCFile(path, label, strict, warn, record, at)
}

func loadTOMLConfigFile(path, label string, strict bool, warn Warn, record bool, scope SchemaScope, at readAt) (*jsonx.OrderedMap, *srcFile, *srcNode, error) {
	if warn == nil {
		warn = defaultWarn
	}
	data, rel, err := at.ws.readWithInput(path, at.input, at.inputUsed, at.role, at.witness, configdoc.TOML)
	if err != nil {
		if os.IsNotExist(err) {
			return jsonx.NewOrderedMap(), nil, nil, nil
		}
		m, parseErr := handleParseFailure(label, err, strict, warn)
		return m, nil, nil, parseErr
	}
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		m, parseErr := handleParseFailure(label, err, strict, warn)
		return m, nil, nil, parseErr
	}
	if err := normalizeTOMLNumbers(doc.Values); err != nil {
		return nil, nil, nil, err
	}
	if err := consumeReadableFormat(doc.Values, label, path); err != nil {
		return nil, nil, nil, err
	}
	if err := validateReadableTOML(doc.Values, scope, label, doc.Index); err != nil {
		return nil, nil, nil, err
	}
	file := &srcFile{path: path, data: data, format: configdoc.TOML, document: doc,
		contained: rel != "", rel: rel, via: at.via, seq: at.ws.nextSeq(), role: at.role}
	if !record {
		return doc.Values, file, readableResetTree(doc.Values, nil), nil
	}
	node := fileTree(file, doc.Values)
	if rel == "" && isGeneratedUserScope(path) {
		if origins := generatedOrigins(data); origins != nil {
			return doc.Values, file, launchCopyTree(file, doc.Values, origins), nil
		}
	}
	markReadableRemovalValues(node, doc.Values, nil)
	return doc.Values, file, node, nil
}

func loadProspectiveTOMLConfigFile(path, label string, strict bool, warn Warn, record bool, scope SchemaScope, at readAt) (*jsonx.OrderedMap, *srcFile, *srcNode, error) {
	input := at.input
	if input == nil || input.prospective == nil || !input.prospective.decodeCandidate || input.prospective.selected == nil ||
		!sameProspectiveSelection(input.prospective.bound, input.prospective.selected) {
		return nil, nil, nil, fmt.Errorf("prospective TOML config read lacks a selected absence receipt")
	}
	if err := verifyProspectiveSelection(input.prospective.selected); err != nil {
		return nil, nil, nil, err
	}
	if filepath.Clean(path) != input.prospective.selected.logical || at.role != input.prospective.selected.role || at.via != "" {
		return nil, nil, nil, fmt.Errorf("prospective TOML config candidate does not match the selected top-level role")
	}
	data := append([]byte(nil), input.prospective.candidate...)
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		m, parseErr := handleParseFailure(label, err, strict, warn)
		return m, nil, nil, parseErr
	}
	if err := normalizeTOMLNumbers(doc.Values); err != nil {
		return nil, nil, nil, err
	}
	if err := consumeReadableFormat(doc.Values, label, path); err != nil {
		return nil, nil, nil, err
	}
	if err := validateReadableTOML(doc.Values, scope, label, doc.Index); err != nil {
		return nil, nil, nil, err
	}
	rel := ""
	if at.ws != nil {
		rel, _ = at.ws.rel(path)
	}
	file := &srcFile{path: path, data: data, format: configdoc.TOML, document: doc,
		contained: rel != "", rel: rel, via: at.via, seq: at.ws.nextSeq(), role: at.role}
	if !record {
		return doc.Values, file, readableResetTree(doc.Values, nil), nil
	}
	node := fileTree(file, doc.Values)
	markReadableRemovalValues(node, doc.Values, nil)
	return doc.Values, file, node, nil
}

func normalizeTOMLNumbers(value any) error {
	switch current := value.(type) {
	case *jsonx.OrderedMap:
		for _, key := range current.Keys() {
			v, _ := current.Get(key)
			normalized, err := normalizeTOMLNumber(v)
			if err != nil {
				return err
			}
			current.Set(key, normalized)
		}
	case []any:
		for i, v := range current {
			normalized, err := normalizeTOMLNumber(v)
			if err != nil {
				return err
			}
			current[i] = normalized
		}
	}
	return nil
}

func normalizeTOMLNumber(value any) (any, error) {
	switch current := value.(type) {
	case int64:
		normalized, ok := jsonx.IntLiteral(strconv.FormatInt(current, 10))
		if !ok {
			return nil, fmt.Errorf("invalid TOML integer %d", current)
		}
		return normalized, nil
	case *jsonx.OrderedMap, []any:
		if err := normalizeTOMLNumbers(current); err != nil {
			return nil, err
		}
		return current, nil
	default:
		return value, nil
	}
}

func consumeReadableFormat(values *jsonx.OrderedMap, label, path string) error {
	visibleLabel := termsafe.Visible(label)
	visiblePath := termsafe.Visible(path)
	version, present := values.Get("format")
	if !present {
		return nil
	}
	n, ok := jsonx.AsInt(version)
	if !ok {
		return configErr("%s: format must be the integer 2 (got %s)", visibleLabel, reprAny(version))
	}
	if n > readableConfigFormat {
		return configErr("%s is format %d; this yolo reads format 2. Upgrade with `yolo update`, or `just install` from a checkout. Nothing was started", visibleLabel, n)
	}
	if n != readableConfigFormat {
		backup := termsafe.Visible(shquote.Join([]string{"cp", "-p", path, path + ".backup"}))
		moveAside := termsafe.Visible(shquote.Join([]string{"mv", path, path + ".disabled"}))
		return configErr("%s: format %d is unsupported in a TOML file; TOML config files use format 2. The file being refused is %s. First confirm a non-config backup path does not already exist and preserve it with `%s`; then manually rewrite its contents as TOML format 2, checking every key against the accepted TOML schema before using it. changing or removing the format marker alone is not a conversion. If this is original legacy JSONC/JSON content, retain or restore those bytes under their original legacy JSONC/JSON filename, then move this unsupported TOML file out of active config filenames with `%s` after confirming that non-config destination does not already exist; never rename legacy bytes to `.toml`. No lossless or equivalence-checked migration is available", visibleLabel, n, visiblePath, backup, moveAside)
	}
	values.Delete("format")
	return nil
}

func readableSiblingCollisionError(files []string) error {
	// The one collision yolo makes itself: a migration stopped between placing a converted file
	// and moving its original aside. Finishing it is the step, never choosing by hand.
	for _, file := range files {
		if j := interruptedMigrationBeside(file); j != "" {
			return configErr("config siblings %s exist. %s", strings.Join(files, ", "), InterruptedMigrationRefusal(j))
		}
	}
	absolute := make([]string, len(files))
	for i, file := range files {
		if fullPath, err := filepath.Abs(file); err == nil {
			file = fullPath
		}
		absolute[i] = file
	}
	files = absolute
	display := strings.Join(files, ", ")
	var choices []string
	for i, keep := range files {
		var moves []string
		for j, other := range files {
			if i != j {
				moves = append(moves, guardedReadableSiblingMove(other))
			}
		}
		choices = append(choices, fmt.Sprintf("To keep %s active: `%s`.", keep, strings.Join(moves, " && ")))
	}
	return configErr("config siblings %s exist; config formats are never merged. Choose exactly one file to keep active, first confirm each backup path does not already exist (including dangling symlinks), then run its full-path guarded move command; then rerun the command. Each command refuses an existing backup destination and uses no-clobber moves. %s The original files are preserved; neither file is converted or merged", display, strings.Join(choices, " "))
}

func guardedReadableSiblingMove(path string) string {
	backup := path + ".backup"
	quotedPath, quotedBackup := shquote.Quote(path), shquote.Quote(backup)
	message := shquote.Quote("backup path already exists: " + backup)
	return "if [ ! -e " + quotedBackup + " ] && [ ! -L " + quotedBackup + " ]; then mv -n " + quotedPath + " " + quotedBackup + " && { [ ! -e " + quotedPath + " ] && [ ! -L " + quotedPath + " ]; }; else printf '%s\\n' " + message + " >&2; exit 1; fi"
}

// UserConfigFile is the user config file the format-aware reader reads: `config.toml`, else
// the legacy `config.jsonc` or `config.json`, and `config.toml` when none exists (the file a
// first write creates). toml says whether it is the TOML file. paths.UserConfigPath knows only
// the legacy names, so a reporter naming the user config uses this. On a TOML-beside-legacy
// collision, which the reader refuses, it answers the TOML file.
func UserConfigFile() (path string, toml bool) {
	legacy := paths.UserConfigPath()
	p, _, isTOML, err := resolveUserConfigPath(legacy)
	if err != nil {
		return filepath.Join(filepath.Dir(legacy), paths.UserConfigTOMLName), true
	}
	return p, isTOML
}

func resolveUserConfigPath(legacyPath string) (path, label string, toml bool, err error) {
	dir := filepath.Dir(legacyPath)
	candidates := []string{paths.UserConfigTOMLName, paths.UserConfigLegacyJSONCName, paths.UserConfigLegacyJSONName}
	var found []string
	for _, candidate := range candidates {
		if pathExists(filepath.Join(dir, candidate)) {
			found = append(found, candidate)
		}
	}
	if len(found) > 1 && found[0] == paths.UserConfigTOMLName {
		conflicts := make([]string, len(found))
		for i, name := range found {
			conflicts[i] = filepath.Join(dir, name)
		}
		return "", "", false, readableSiblingCollisionError(conflicts)
	}
	name := paths.UserConfigTOMLName
	if len(found) > 0 {
		name = found[0]
	}
	return filepath.Join(dir, name), filepath.Join(dir, name), name == paths.UserConfigTOMLName, nil
}

func loadUserConfigFragmentsWithInputUse(base *jsonx.OrderedMap, baseNode *srcNode, dir string, strict bool, warn Warn, seen map[string]struct{}, record bool, input *ConfigReadInput, inputUsed *bool) (*jsonx.OrderedMap, *srcNode, error) {
	fragmentDir := filepath.Join(dir, "config.d")
	entries, err := os.ReadDir(fragmentDir)
	if err != nil {
		if os.IsNotExist(err) {
			if base == nil {
				base = jsonx.NewOrderedMap()
			}
			return base, baseNode, nil
		}
		_, parseErr := handleParseFailure(fragmentDir, err, strict, warn)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		if base == nil {
			base = jsonx.NewOrderedMap()
		}
		return base, baseNode, nil
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".toml") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	merged := base
	if merged == nil {
		merged = jsonx.NewOrderedMap()
	}
	mergedNode := baseNode
	for _, name := range names {
		path := filepath.Join(fragmentDir, name)
		info, statErr := os.Stat(path) // Follow conventional symlinked fragments.
		if statErr != nil {
			_, parseErr := handleParseFailure(path, statErr, strict, warn)
			if parseErr != nil {
				return nil, nil, parseErr
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		fragment, node, loadErr := loadWithIncludesScope(path, path, strict, warn, seen, record, SchemaScopeUserOnly, readAt{input: input, inputUsed: inputUsed, role: editRoleUser, witness: currentEditWitness(input)})
		if loadErr != nil {
			return nil, nil, loadErr
		}
		merged, mergedNode = mergeReadableConfig(merged, fragment, mergedNode, node, nil)
	}
	return merged, mergedNode, nil
}

func mergeUserConfigFragmentsWithInputUse(base *jsonx.OrderedMap, baseNode *srcNode, dir string, strict bool, warn Warn, seen map[string]struct{}, record bool, input *ConfigReadInput, inputUsed *bool) (*jsonx.OrderedMap, *srcNode, error) {
	fragments, fragmentNode, err := loadUserConfigFragmentsWithInputUse(base, baseNode, dir, strict, warn, seen, record, input, inputUsed)
	if err != nil {
		return nil, nil, err
	}
	return fragments, fragmentNode, nil
}
