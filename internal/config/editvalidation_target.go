package config

import (
	"bytes"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// EditTargetChoiceKind selects the standard target policy or one explicit config role/file.
type EditTargetChoiceKind uint8

const (
	EditTargetAutomatic EditTargetChoiceKind = iota + 1
	EditTargetUser
	EditTargetProject
	EditTargetLocal
	EditTargetFile
)

// EditTargetChoice is a typed override; File is used only with EditTargetFile.
type EditTargetChoice struct {
	Kind EditTargetChoiceKind
	File string
}

// EditMutationKind identifies the existing splice operation without depending on configedit.
type EditMutationKind uint8

const (
	EditMutationSet EditMutationKind = iota + 1
	EditMutationAdd
	EditMutationAppend
	EditMutationRemove
)

// EditMutationIntent keeps the structured key and operation together; key members are literal,
// including dots, and are never split as a display string. Public configedit callers set
// AllowProspectiveCreation only when the selected standard Project/Local TOML basefile is absent.
type EditMutationIntent struct {
	Key                      []configdoc.Step
	Kind                     EditMutationKind
	AllowProspectiveCreation bool
}

// ResolvedEditTarget is a detached target binding. ExpectedBytes returns a copy of the
// preimage; it is discovery evidence, not a semantic validation result.
type ResolvedEditTarget struct {
	LogicalPath  string
	PhysicalPath string
	Format       configdoc.Format
	Role         ExplicitEditScope
	Target       ExplicitEditTarget
	expected     []byte
	intent       EditMutationIntent
	authored     []configdoc.Step
	prospective  *prospectiveSelectionReceipt
}

// IsProspective reports that this binding records selected absence rather than an existing preimage.
func (r ResolvedEditTarget) IsProspective() bool { return r.prospective != nil }

// VerifyProspectiveAbsence rechecks the public coordinates against the private receipt, then
// rechecks the bound parent identity and absent destination leaf.
func (r ResolvedEditTarget) VerifyProspectiveAbsence() error {
	if err := r.verifyProspectiveBinding(); err != nil {
		return err
	}
	return verifyProspectiveSelection(r.prospective)
}

// VerifyProspectiveParent rechecks the target binding and bound directory identity after publication.
func (r ResolvedEditTarget) VerifyProspectiveParent() error {
	if err := r.verifyProspectiveBinding(); err != nil {
		return err
	}
	return verifyProspectiveParent(r.prospective)
}

func (r ResolvedEditTarget) verifyProspectiveBinding() error {
	receipt := r.prospective
	if receipt == nil {
		return fmt.Errorf("config target is not a prospective creation binding")
	}
	scope := prospectiveScope(receipt.role)
	if scope == 0 || r.LogicalPath != receipt.logical || r.PhysicalPath != receipt.logical ||
		r.Role != scope || r.Format != receipt.format || r.Target.workspace != receipt.workspace ||
		r.Target.physical != receipt.logical || r.Target.scope != scope || r.Target.format != receipt.format ||
		!sameProspectiveSelection(receipt, r.Target.prospective) {
		return fmt.Errorf("prospective config target coordinates no longer match the private selected-absence receipt; resolve the target again and retry")
	}
	return nil
}

// ValidateProspective parses and validates candidate bytes through the standard selected layer.
// Its private selection receipt is distinct from an actual-file read witness.
func (r ResolvedEditTarget) ValidateProspective(workspace string, candidate []byte) (ValidatedExplicitEditResult, error) {
	return r.ValidateProspectiveTolerating(workspace, candidate, nil)
}

// ValidateProspectiveTolerating is ValidateProspective, except that a problem among preexisting
// (the problems the config had before this edit, EditProblems) is reported in the result's
// Preexisting rather than refusing it.
func (r ResolvedEditTarget) ValidateProspectiveTolerating(workspace string, candidate []byte, preexisting []string) (ValidatedExplicitEditResult, error) {
	if err := r.verifyProspectiveBinding(); err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	input := newProspectiveCandidateInput(r.prospective, candidate)
	return validateProspectiveEdit(workspace, r.Target, input, preexisting)
}

// ExpectedBytes returns a detached copy of the selected preimage. Existing empty files remain
// non-nil empty; prospective absence also returns nil, distinguished by IsProspective.
func (r ResolvedEditTarget) ExpectedBytes() []byte { return cloneEditTargetBytes(r.expected) }

// AuthoredPath returns a detached path bound from the consumed source selected for this target.
// It never changes the original structured mutation intent checked by MatchesMutation.
func (r ResolvedEditTarget) AuthoredPath() []configdoc.Step {
	return append([]configdoc.Step(nil), r.authored...)
}

func cloneEditTargetBytes(source []byte) []byte {
	if source == nil {
		return nil
	}
	cloned := make([]byte, len(source))
	copy(cloned, source)
	return cloned
}

// MatchesMutation reports whether a public consumer is applying the exact resolved key/operation.
func (r ResolvedEditTarget) MatchesMutation(intent EditMutationIntent) bool {
	if r.intent.Kind != intent.Kind || len(r.intent.Key) != len(intent.Key) {
		return false
	}
	for i := range intent.Key {
		if r.intent.Key[i] != intent.Key[i] {
			return false
		}
	}
	return true
}

// ResolveEditTarget selects one already-existing file consumed by the normal readable config
// graph. Schema scope supplies only the default; ApplyEffective remains the write authority.
func ResolveEditTarget(workspace string, intent EditMutationIntent, choice EditTargetChoice) (ResolvedEditTarget, error) {
	return resolveEditTarget(workspace, intent, choice, os.ReadFile)
}

func resolveEditTarget(workspace string, intent EditMutationIntent, choice EditTargetChoice, readPreimage func(string) ([]byte, error)) (ResolvedEditTarget, error) {
	if readPreimage == nil {
		return ResolvedEditTarget{}, fmt.Errorf("config edit target requires a preimage reader")
	}
	key := intent.Key
	if intent.Kind < EditMutationSet || intent.Kind > EditMutationRemove {
		return ResolvedEditTarget{}, fmt.Errorf("config edit target requires a supported mutation intent")
	}
	if len(key) == 0 {
		return ResolvedEditTarget{}, fmt.Errorf("config edit target requires a structured key")
	}
	parts := make([]string, len(key))
	for i, step := range key {
		if step.IsIndex {
			if step.Key != "" || step.Index < 0 {
				return ResolvedEditTarget{}, fmt.Errorf("config edit target contains a malformed array index")
			}
			parts[i] = "*"
			continue
		}
		if step.Key == "" || step.Index != 0 {
			return ResolvedEditTarget{}, fmt.Errorf("config edit target contains a malformed object member")
		}
		parts[i] = step.Key
	}
	rule, ok := readableEditTargetRule(parts)
	if !ok && intent.Kind == EditMutationRemove {
		rule, ok = legacyOffSwitchRule(parts)
	}
	if !ok && intent.Kind == EditMutationRemove {
		rule, ok = removedSetupKeyRule(parts)
	}
	if !ok {
		return ResolvedEditTarget{}, fmt.Errorf("config key %s has no readable schema target%s", strings.Join(parts, "."), renamedKeyHint(parts))
	}
	if workspace == "" {
		workspace = cwd()
	}
	resolvedWorkspace, err := resolvedAbsolutePath(workspace)
	if err != nil {
		return ResolvedEditTarget{}, fmt.Errorf("resolve config edit workspace: %w", err)
	}
	role := ExplicitEditProject
	switch choice.Kind {
	case EditTargetAutomatic:
		switch rule.Scope {
		case SchemaScopeUserOnly:
			role = ExplicitEditUser
		case SchemaScopeBoth, SchemaScopeWorkspaceOnly, SchemaScopeFieldSensitive:
			role = ExplicitEditProject
		default:
			return ResolvedEditTarget{}, fmt.Errorf("config key %s has no editable scope", strings.Join(parts, "."))
		}
	case EditTargetUser:
		role = ExplicitEditUser
	case EditTargetProject:
		role = ExplicitEditProject
	case EditTargetLocal:
		role = ExplicitEditLocal
	case EditTargetFile:
		if choice.File == "" {
			return ResolvedEditTarget{}, fmt.Errorf("named config edit target requires a file path")
		}
	default:
		return ResolvedEditTarget{}, fmt.Errorf("config edit target choice is invalid")
	}
	if choice.Kind != EditTargetFile && rule.Scope == SchemaScopeUserOnly && role != ExplicitEditUser {
		return ResolvedEditTarget{}, fmt.Errorf("config.%s is user-scope only; select a User config file", strings.Join(parts, "."))
	}
	if choice.Kind != EditTargetFile && rule.Scope == SchemaScopeWorkspaceOnly && role == ExplicitEditUser {
		return ResolvedEditTarget{}, fmt.Errorf("config.%s is workspace-only; move requests to the workspace config", strings.Join(parts, "."))
	}

	var roleAudit []editTargetReadObservation
	discoveryInput := &ConfigReadInput{roleAudit: &roleAudit}
	if intent.AllowProspectiveCreation && choice.Kind != EditTargetFile && (role == ExplicitEditProject || role == ExplicitEditLocal) {
		name := WorkspaceConfigName
		if role == ExplicitEditLocal {
			name = WorkspaceLocalConfigName
		}
		path, _, pathErr := resolveWorkspaceConfigPath(resolvedWorkspace, name)
		if pathErr != nil {
			return ResolvedEditTarget{}, pathErr
		}
		if strings.EqualFold(filepath.Ext(path), ".toml") && !pathExists(path) {
			discoveryInput.prospective = newProspectiveSelectionInput(resolvedWorkspace, path, prospectiveRole(role)).prospective
		}
	}
	var node *srcNode
	if discoveryInput.prospective == nil {
		_, node, err = composeReadableAuthoredConfigWithInputUseAudit(resolvedWorkspace, true, nil, true, true, nil, nil, &roleAudit)
	} else {
		_, node, err = composeReadableAuthoredConfigWithInputUseAudit(resolvedWorkspace, true, nil, true, true, discoveryInput, nil, nil)
	}
	if err != nil {
		return ResolvedEditTarget{}, fmt.Errorf("read config graph for edit target: %w", err)
	}
	files, err := editTargetFiles(sourcesOf(node), roleAudit)
	if err != nil {
		return ResolvedEditTarget{}, err
	}

	var selected editTargetFile
	var prospective *prospectiveSelectionReceipt
	authored := append([]configdoc.Step(nil), key...)
	if choice.Kind == EditTargetFile {
		physical, resolveErr := filepath.EvalSymlinks(choice.File)
		if resolveErr != nil {
			return ResolvedEditTarget{}, fmt.Errorf("resolve named config file %s: %w", choice.File, resolveErr)
		}
		physical, resolveErr = filepath.Abs(physical)
		if resolveErr != nil {
			return ResolvedEditTarget{}, resolveErr
		}
		selected, ok = files[filepath.Clean(physical)]
		if !ok {
			return ResolvedEditTarget{}, fmt.Errorf("named config file %s is not consumed by the scoped config graph", choice.File)
		}
		if len(selected.roles) != 1 || len(selected.formats) != 1 {
			return ResolvedEditTarget{}, fmt.Errorf("named config file %s has conflicting reader roles or formats", choice.File)
		}
		namedPath, resolveErr := filepath.Abs(choice.File)
		if resolveErr != nil {
			return ResolvedEditTarget{}, resolveErr
		}
		namedPath = filepath.Clean(namedPath)
		if !selected.logicalPaths[namedPath] && namedPath != selected.physical {
			return ResolvedEditTarget{}, fmt.Errorf("named config file %s was not read as a logical file by the scoped config graph", choice.File)
		}
		if selected.logicalPaths[namedPath] {
			selected.logical = namedPath
		}
		role, ok = scopeForReadRole(selected.role())
		if !ok {
			return ResolvedEditTarget{}, fmt.Errorf("named config file %s has no editable reader role", choice.File)
		}
		if rule.Scope == SchemaScopeUserOnly && role != ExplicitEditUser {
			return ResolvedEditTarget{}, fmt.Errorf("config.%s is user-scope only; named file %s is not User config", strings.Join(parts, "."), choice.File)
		}
		if rule.Scope == SchemaScopeWorkspaceOnly && role == ExplicitEditUser {
			return ResolvedEditTarget{}, fmt.Errorf("config.%s is workspace-only; move requests to the workspace config", strings.Join(parts, "."))
		}
	} else if role == ExplicitEditUser {
		var mapped bool
		bindAuthoredPath := choice.Kind == EditTargetAutomatic && editTargetPathHasIndex(key)
		selected, authored, mapped, err = userSectionTarget(sourcesOf(node), key, files, bindAuthoredPath)
		if err != nil {
			return ResolvedEditTarget{}, err
		}
		if selected.physical == "" {
			path, _, _, pathErr := resolveUserConfigPath(paths.UserConfigPath())
			if pathErr != nil {
				return ResolvedEditTarget{}, pathErr
			}
			selected, err = consumedDefaultTarget(path, ExplicitEditUser, files)
			if err != nil {
				return ResolvedEditTarget{}, err
			}
			authored = append([]configdoc.Step(nil), key...)
		}
		if choice.Kind == EditTargetAutomatic && !mapped && editTargetPathHasIndex(key) {
			return ResolvedEditTarget{}, fmt.Errorf("user config key %s has no consumed authored source for its indexed target", strings.Join(parts, "."))
		}
	} else {
		name := WorkspaceConfigName
		if role == ExplicitEditLocal {
			name = WorkspaceLocalConfigName
		}
		path, _, pathErr := resolveWorkspaceConfigPath(resolvedWorkspace, name)
		if pathErr != nil {
			return ResolvedEditTarget{}, pathErr
		}
		selected, err = consumedDefaultTarget(path, role, files)
		if err != nil {
			receipt := discoveryInput.prospective
			if receipt == nil || receipt.selected == nil || receipt.selected.logical != filepath.Clean(path) || receipt.selected.role != prospectiveRole(role) {
				return ResolvedEditTarget{}, err
			}
			prospective = copyProspectiveSelection(receipt.selected)
			if verifyErr := verifyProspectiveSelection(prospective); verifyErr != nil {
				return ResolvedEditTarget{}, verifyErr
			}
			selected = editTargetFile{
				logical: prospective.logical, physical: prospective.logical,
				logicalPaths: map[string]bool{prospective.logical: true},
				roles:        map[editReadRole]bool{prospective.role: true},
				formats:      map[configdoc.Format]bool{prospective.format: true},
			}
		}
	}
	if len(selected.roles) != 1 || len(selected.formats) != 1 || selected.role() != readRoleForScope(role) {
		return ResolvedEditTarget{}, fmt.Errorf("config target %s was not uniquely read as %s config", selected.logical, roleName(role))
	}
	format := selected.format()
	var target ExplicitEditTarget
	var preimage []byte
	if prospective != nil {
		if format != configdoc.TOML || selected.physical != prospective.logical {
			return ResolvedEditTarget{}, fmt.Errorf("prospective config target has a conflicting reader format or identity")
		}
		target = newProspectiveEditTarget(resolvedWorkspace, prospective)
	} else {
		if _, err := os.Stat(selected.physical); err != nil {
			return ResolvedEditTarget{}, fmt.Errorf("config target %s does not exist; create that config file first, then retry (no file was written): %w", selected.logical, err)
		}
		preimage, err = readPreimage(selected.physical)
		if err != nil {
			return ResolvedEditTarget{}, fmt.Errorf("read config target %s: %w", selected.logical, err)
		}
		if (intent.Kind == EditMutationSet || intent.Kind == EditMutationRemove) && format == configdoc.JSONC {
			mapped, found, mapErr := editTargetJSONCAuthoredPath(node, key, selected, role, preimage)
			if mapErr != nil {
				return ResolvedEditTarget{}, mapErr
			}
			if found {
				authored = mapped
			}
		}
		target, err = NewExplicitEditTarget(resolvedWorkspace, selected.physical, role, format)
		if err != nil {
			return ResolvedEditTarget{}, err
		}
	}
	return ResolvedEditTarget{LogicalPath: selected.logical, PhysicalPath: selected.physical, Format: format, Role: role, Target: target,
		expected: cloneEditTargetBytes(preimage), intent: cloneEditMutationIntent(intent), authored: append([]configdoc.Step(nil), authored...),
		prospective: copyProspectiveSelection(prospective)}, nil
}

func editTargetJSONCAuthoredPath(root *srcNode, key []configdoc.Step, selected editTargetFile, role ExplicitEditScope, preimage []byte) ([]configdoc.Step, bool, error) {
	if err := rejectChangedSelectedJSONCConsumedSource(sourcesOf(root), selected, role, preimage); err != nil {
		return nil, false, err
	}
	node := root
	for _, step := range key {
		if step.IsIndex {
			node = node.elem(step.Index)
		} else {
			node = node.key(step.Key)
		}
		if node == nil {
			return nil, false, nil
		}
	}
	wantedRole := readRoleForScope(role)
	var authored []configdoc.Step
	for _, origin := range node.writerList() {
		f := origin.file
		if f == nil || origin.host != "" || f.role != wantedRole || f.format != configdoc.JSONC {
			continue
		}
		physical, err := filepath.EvalSymlinks(f.path)
		if err != nil {
			return nil, false, fmt.Errorf("resolve JSONC edit source %s: %w", f.path, err)
		}
		physical, err = filepath.Abs(physical)
		if err != nil {
			return nil, false, err
		}
		if filepath.Clean(physical) != selected.physical {
			continue
		}
		if !bytes.Equal(f.data, preimage) {
			return nil, false, fmt.Errorf("config target %s changed between source discovery and target binding; reread the config and retry (no file was written)", selected.logical)
		}
		if _, present := f.locate(origin.steps, origin.documentSteps); !present {
			return nil, false, fmt.Errorf("selected JSONC edit source %s has no unique authored value at %s", selected.logical, strings.Join(stepPathNames(origin.documentSteps), "."))
		}
		candidate := append([]configdoc.Step(nil), origin.documentSteps...)
		if authored != nil && !sameEditTargetPath(authored, candidate) {
			return nil, false, fmt.Errorf("selected JSONC edit source %s has ambiguous authored paths for %s", selected.logical, strings.Join(stepPathNames(key), "."))
		}
		authored = candidate
	}
	if authored == nil {
		return nil, false, nil
	}
	return authored, true, nil
}

func rejectChangedSelectedJSONCConsumedSource(sources *Sources, selected editTargetFile, role ExplicitEditScope, preimage []byte) error {
	if sources == nil || sources.root == nil {
		return fmt.Errorf("config target %s has no consumed source bytes; reread the config and retry (no file was written)", selected.logical)
	}
	wantedRole := readRoleForScope(role)
	seenNodes := map[*srcNode]bool{}
	seenFiles := map[*srcFile]bool{}
	found := false
	var walk func(*srcNode) error
	walk = func(node *srcNode) error {
		if node == nil || seenNodes[node] {
			return nil
		}
		seenNodes[node] = true
		for _, origin := range node.writers {
			f := origin.file
			if f == nil || origin.host != "" || f.role != wantedRole || f.format != configdoc.JSONC || seenFiles[f] {
				continue
			}
			seenFiles[f] = true
			physical, err := filepath.EvalSymlinks(f.path)
			if err != nil {
				return fmt.Errorf("resolve consumed JSONC edit source %s: %w", f.path, err)
			}
			physical, err = filepath.Abs(physical)
			if err != nil {
				return err
			}
			if filepath.Clean(physical) != selected.physical {
				continue
			}
			found = true
			if !bytes.Equal(f.data, preimage) {
				return fmt.Errorf("config target %s changed between source discovery and target binding; reread the config and retry (no file was written)", selected.logical)
			}
		}
		for _, child := range node.keys {
			if err := walk(child); err != nil {
				return err
			}
		}
		for _, child := range node.elems {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(sources.root); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("config target %s has no matching consumed JSONC source bytes; reread the config and retry (no file was written)", selected.logical)
	}
	return nil
}

func readableEditTargetRule(path []string) (SchemaKey, bool) {
	if rule, ok := readableSchemaRule(path); ok {
		return rule, true
	}
	for length := len(path) - 1; length > 0; length-- {
		ancestor, ok := readableSchemaRule(path[:length])
		if ok && ancestor.Type == SchemaTypeAny {
			return ancestor, true
		}
	}
	return SchemaKey{}, false
}

func cloneEditMutationIntent(intent EditMutationIntent) EditMutationIntent {
	intent.Key = append([]configdoc.Step(nil), intent.Key...)
	return intent
}

type editTargetReadObservation struct {
	path string
	role editReadRole
}

type editTargetFile struct {
	logical      string
	physical     string
	logicalPaths map[string]bool
	roles        map[editReadRole]bool
	formats      map[configdoc.Format]bool
}

func (f editTargetFile) role() editReadRole {
	for role := range f.roles {
		return role
	}
	return editRoleUnspecified
}

func (f editTargetFile) format() configdoc.Format {
	for format := range f.formats {
		return format
	}
	return 0
}

func consumedDefaultTarget(path string, scope ExplicitEditScope, files map[string]editTargetFile) (editTargetFile, error) {
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return editTargetFile{}, fmt.Errorf("config target %s does not exist; create that config file first, then retry (no file was written)", path)
		}
		return editTargetFile{}, fmt.Errorf("resolve default config target %s: %w", path, err)
	}
	physical, err = filepath.Abs(physical)
	if err != nil {
		return editTargetFile{}, err
	}
	physical = filepath.Clean(physical)
	selected, ok := files[physical]
	if !ok {
		return editTargetFile{}, fmt.Errorf("default config target %s exists but was not consumed by the scoped config graph", path)
	}
	if len(selected.roles) != 1 || len(selected.formats) != 1 || selected.role() != readRoleForScope(scope) {
		return editTargetFile{}, fmt.Errorf("default config target %s has conflicting or unauthorized reader roles/formats", path)
	}
	return selected, nil
}

func editTargetFiles(sources *Sources, audit []editTargetReadObservation) (map[string]editTargetFile, error) {
	files := map[string]editTargetFile{}
	seen := map[*srcNode]bool{}
	var walk func(*srcNode) error
	walk = func(node *srcNode) error {
		if node == nil || seen[node] {
			return nil
		}
		seen[node] = true
		for _, origin := range node.writers {
			f := origin.file
			if f == nil || f.role == editRoleUnspecified || origin.host != "" {
				continue
			}
			physical, err := filepath.EvalSymlinks(f.path)
			if err != nil {
				return fmt.Errorf("resolve consumed config path %s: %w", f.path, err)
			}
			physical, err = filepath.Abs(physical)
			if err != nil {
				return err
			}
			physical = filepath.Clean(physical)
			entry := files[physical]
			if entry.physical == "" {
				entry = editTargetFile{logical: f.path, physical: physical, logicalPaths: map[string]bool{}, roles: map[editReadRole]bool{}, formats: map[configdoc.Format]bool{}}
			}
			logical, err := filepath.Abs(f.path)
			if err != nil {
				return err
			}
			entry.logicalPaths[filepath.Clean(logical)] = true
			entry.roles[f.role] = true
			entry.formats[f.format] = true
			files[physical] = entry
		}
		for _, child := range node.keys {
			if err := walk(child); err != nil {
				return err
			}
		}
		for _, child := range node.elems {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if sources != nil {
		if err := walk(sources.root); err != nil {
			return nil, err
		}
	}
	for _, observation := range audit {
		physical, err := filepath.EvalSymlinks(observation.path)
		if err != nil {
			return nil, fmt.Errorf("resolve observed config path %s: %w", observation.path, err)
		}
		physical, err = filepath.Abs(physical)
		if err != nil {
			return nil, err
		}
		physical = filepath.Clean(physical)
		entry, ok := files[physical]
		if !ok {
			continue
		} // attempts can veto a consumed target but never establish consumption
		entry.roles[observation.role] = true
		files[physical] = entry
	}
	return files, nil
}

func userSectionTarget(sources *Sources, key []configdoc.Step, files map[string]editTargetFile, bindAuthoredPath bool) (editTargetFile, []configdoc.Step, bool, error) {
	node := sources.node()
	type ancestor struct {
		node       *srcNode
		pathLength int
	}
	var ancestors []ancestor
	for i, step := range key {
		if step.IsIndex {
			node = node.elem(step.Index)
		} else {
			node = node.key(step.Key)
		}
		if node == nil {
			break
		}
		ancestors = append(ancestors, ancestor{node: node, pathLength: i + 1})
	}
	lastIndex := -1
	for i, step := range key {
		if step.IsIndex {
			lastIndex = i
		}
	}
	for n := len(ancestors) - 1; n >= 0; n-- {
		selected := ancestors[n].node
		for i := len(selected.writers) - 1; i >= 0; i-- {
			origin := selected.writers[i]
			f := origin.file
			if f == nil || f.role != editRoleUser || origin.host != "" {
				continue
			}
			physical, err := filepath.EvalSymlinks(f.path)
			if err != nil {
				return editTargetFile{}, nil, false, fmt.Errorf("resolve User section source %s: %w", f.path, err)
			}
			physical, err = filepath.Abs(physical)
			if err != nil {
				return editTargetFile{}, nil, false, err
			}
			physical = filepath.Clean(physical)
			entry, ok := files[physical]
			if !ok {
				return editTargetFile{}, nil, false, fmt.Errorf("user section source %s was not consumed by the scoped graph", f.path)
			}
			entry.logical = f.path
			if !bindAuthoredPath {
				return entry, append([]configdoc.Step(nil), key...), false, nil
			}
			if lastIndex >= 0 {
				if ancestors[n].pathLength <= lastIndex {
					return editTargetFile{}, nil, false, fmt.Errorf("user indexed target has no consumed authored element at %s", strings.Join(stepPathNames(key), "."))
				}
				if err := requireUniqueIndexedOrigin(selected); err != nil {
					return editTargetFile{}, nil, false, err
				}
				if countEditTargetIndices(origin.documentSteps) < countEditTargetIndices(key[:ancestors[n].pathLength]) {
					return editTargetFile{}, nil, false, fmt.Errorf("user indexed target has no authored index mapping at %s", strings.Join(stepPathNames(key), "."))
				}
			}
			authored := append([]configdoc.Step(nil), origin.documentSteps...)
			for _, step := range key[ancestors[n].pathLength:] {
				if step.IsIndex {
					return editTargetFile{}, nil, false, fmt.Errorf("user target has no consumed authored source for an indexed suffix")
				}
				authored = append(authored, step)
			}
			return entry, authored, true, nil
		}
	}
	return editTargetFile{}, nil, false, nil
}

func editTargetPathHasIndex(path []configdoc.Step) bool {
	return countEditTargetIndices(path) != 0
}

func countEditTargetIndices(path []configdoc.Step) int {
	count := 0
	for _, step := range path {
		if step.IsIndex {
			count++
		}
	}
	return count
}

func stepPathNames(path []configdoc.Step) []string {
	parts := make([]string, len(path))
	for i, step := range path {
		if step.IsIndex {
			parts[i] = fmt.Sprintf("[%d]", step.Index)
		} else {
			parts[i] = step.Key
		}
	}
	return parts
}

func requireUniqueIndexedOrigin(node *srcNode) error {
	var physicalPath string
	var documentPath []configdoc.Step
	for _, origin := range node.writers {
		f := origin.file
		if f == nil || f.role != editRoleUser || origin.host != "" {
			continue
		}
		physical, err := filepath.EvalSymlinks(f.path)
		if err != nil {
			return fmt.Errorf("resolve User indexed source %s: %w", f.path, err)
		}
		physical, err = filepath.Abs(physical)
		if err != nil {
			return err
		}
		physical = filepath.Clean(physical)
		if physicalPath == "" {
			physicalPath = physical
			documentPath = append([]configdoc.Step(nil), origin.documentSteps...)
			continue
		}
		if physical != physicalPath || !sameEditTargetPath(origin.documentSteps, documentPath) {
			return fmt.Errorf("user indexed element has ambiguous authored sources; refusing to choose one")
		}
	}
	return nil
}

func sameEditTargetPath(a, b []configdoc.Step) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func scopeForReadRole(role editReadRole) (ExplicitEditScope, bool) {
	switch role {
	case editRoleUser:
		return ExplicitEditUser, true
	case editRoleProject:
		return ExplicitEditProject, true
	case editRoleLocal:
		return ExplicitEditLocal, true
	default:
		return 0, false
	}
}

func readRoleForScope(scope ExplicitEditScope) editReadRole {
	switch scope {
	case ExplicitEditUser:
		return editRoleUser
	case ExplicitEditProject:
		return editRoleProject
	case ExplicitEditLocal:
		return editRoleLocal
	default:
		return editRoleUnspecified
	}
}

func roleName(scope ExplicitEditScope) string {
	switch scope {
	case ExplicitEditUser:
		return "User"
	case ExplicitEditProject:
		return "Project"
	case ExplicitEditLocal:
		return "Local"
	default:
		return "unknown"
	}
}

// legacyOffSwitchRule is the edit rule for removing a legacy `loopholes.<name>.enabled` of an
// off-switch loophole (docs/design/legacy-config-compat.md LC-D3), the switch a legacy JSONC file
// keeps the loophole off with and the key the launch's disclosure names to turn it back on. Only
// removal: the key has no TOML spelling (that is `allow.<pack>.off`), and a TOML file never holds
// it, so the unset of a TOML target finds no rule there and refuses as before.
// removedSetupKeyRule is the removal target of a key named setups replace (RemovedSetupKeys):
// every refusal of one ends in `yolo config unset <key> --user|--project|--local`, so an unset
// reaches it in whichever file holds it, JSONC or TOML. Only a removal: nothing sets one.
func removedSetupKeyRule(parts []string) (SchemaKey, bool) {
	if len(parts) != 1 || !IsRemovedSetupKey(parts[0]) {
		return SchemaKey{}, false
	}
	return SchemaKey{Path: parts[0], Scope: SchemaScopeBoth, Type: SchemaTypeString}, true
}

func legacyOffSwitchRule(parts []string) (SchemaKey, bool) {
	if len(parts) != 3 || parts[0] != "loopholes" || parts[2] != "enabled" || !loopholedecl.HasOffSwitch(parts[1]) {
		return SchemaKey{}, false
	}
	return SchemaKey{Path: "loopholes.*.enabled", Scope: SchemaScopeBoth, Type: SchemaTypeBoolean}, true
}
