package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ExplicitEditScope is the normal config-graph role the caller intends to edit. Its zero value
// is deliberately invalid; provenance and filenames never grant write authority.
type ExplicitEditScope uint8

const (
	ExplicitEditUser ExplicitEditScope = iota + 1
	ExplicitEditProject
	ExplicitEditLocal
)

type editReadRole uint8

func (r editReadRole) String() string {
	switch r {
	case editRoleUser:
		return "user"
	case editRoleProject:
		return "project"
	case editRoleLocal:
		return "local"
	case editRoleInherited:
		return "inherited"
	default:
		return "unspecified"
	}
}

const (
	editRoleUnspecified editReadRole = iota
	editRoleUser
	editRoleProject
	editRoleLocal
	editRoleInherited
)

type editReadWitness struct {
	reads []editReadObservation
}

type editReadObservation struct {
	role   editReadRole
	format configdoc.Format
}

func (w *editReadWitness) record(role editReadRole, format configdoc.Format) {
	if w == nil {
		return
	}
	for _, prior := range w.reads {
		if prior == (editReadObservation{role: role, format: format}) {
			return
		}
	}
	w.reads = append(w.reads, editReadObservation{role: role, format: format})
}

func (w *editReadWitness) observeSeenPath(path string, input *ConfigReadInput, role editReadRole) {
	if w == nil || input == nil || role == editRoleUnspecified {
		return
	}
	opened, err := os.Stat(path)
	if err != nil {
		return
	}
	target, err := os.Stat(input.target)
	if err != nil || !os.SameFile(opened, target) {
		return
	}
	w.record(role, configFormatOf(path))
}

func currentEditWitness(input *ConfigReadInput) *editReadWitness {
	if input == nil {
		return nil
	}
	return input.witness
}

// ExplicitEditTarget binds caller-declared scope, workspace, physical file and decoder format.
// Fields are private so a zero or partially filled target cannot acquire authority.
type ExplicitEditTarget struct {
	workspace   string
	physical    string
	scope       ExplicitEditScope
	format      configdoc.Format
	prospective *prospectiveSelectionReceipt
}

// IsProspective reports a private selected-absence receipt for a workspace TOML basefile.
func (t ExplicitEditTarget) IsProspective() bool { return t.prospective != nil }

// NewExplicitEditTarget constructs a complete explicit edit binding. physicalTarget must name
// the physical file used by the edit transaction, not a source label or inferred origin.
func NewExplicitEditTarget(workspace, physicalTarget string, scope ExplicitEditScope, format configdoc.Format) (ExplicitEditTarget, error) {
	if scope != ExplicitEditUser && scope != ExplicitEditProject && scope != ExplicitEditLocal {
		return ExplicitEditTarget{}, fmt.Errorf("explicit edit scope is required and must be user, project, or local")
	}
	if format != configdoc.JSONC && format != configdoc.TOML {
		return ExplicitEditTarget{}, fmt.Errorf("explicit edit format must be JSONC or TOML")
	}
	if !filepath.IsAbs(physicalTarget) {
		return ExplicitEditTarget{}, fmt.Errorf("explicit edit target must be an absolute physical path: %q", physicalTarget)
	}
	physical := filepath.Clean(physicalTarget)
	resolved, err := filepath.EvalSymlinks(physical)
	if err != nil {
		return ExplicitEditTarget{}, fmt.Errorf("resolve explicit edit target %s: %w", physical, err)
	}
	if filepath.Clean(resolved) != physical {
		return ExplicitEditTarget{}, fmt.Errorf("explicit edit target must be the resolved physical path: %s", resolved)
	}
	workspace, err = resolvedAbsolutePath(workspace)
	if err != nil {
		return ExplicitEditTarget{}, fmt.Errorf("resolve explicit edit workspace: %w", err)
	}
	return ExplicitEditTarget{workspace: workspace, physical: physical, scope: scope, format: format}, nil
}

type editValidationContext struct {
	workspace        string
	target           ExplicitEditTarget
	input            ConfigReadInput
	witness          *editReadWitness
	embeddedFS       fs.FS
	embeddedNames    []string
	embeddedNamesErr error
	effective        *jsonx.OrderedMap
	rawUser          *jsonx.OrderedMap
	rawWorkspace     *jsonx.OrderedMap
	userOrigins      *Sources
	workspaceOrigins *Sources
	reads            []editReadObservation
	packs            []*packload.Pack
	packNotes        []string
	catalog          map[string]map[string]packdecl.ProviderModel
	semantic         *ReadableSemanticInputs
	viaSelectionErr  error
	viaProfiles      map[string]packload.UserProfile
	prospective      bool
}

type ValidatedReadableRequest struct {
	Path       string
	Files      []string
	Capability string
	Status     ReadableRequestStatus
	Reason     string
}

type ValidatedExplicitEditResult struct {
	snapshot         string
	warnings         []string
	readableWarnings []string
	readableRequests []ValidatedReadableRequest
	unverifiedPacks  []string
	preexisting      []string
	validated        bool
}

// UnverifiedPacks names, one line each, the configured packs a jail's workspace-config edit
// could not read (a host-only source with no delivered copy) and so did not check, each line
// naming where they are checked. Empty on the host, where such a pack refuses the edit instead.
func (r ValidatedExplicitEditResult) UnverifiedPacks() []string {
	return append([]string(nil), r.unverifiedPacks...)
}

// SnapshotJSON returns the validated planned snapshot and false for a zero, unvalidated result.
func (r ValidatedExplicitEditResult) SnapshotJSON() (string, bool) {
	if !r.validated {
		return "", false
	}
	return r.snapshot, true
}

// Warnings returns a detached copy of warnings from the unchanged common validator.
func (r ValidatedExplicitEditResult) Warnings() []string {
	return append([]string(nil), r.warnings...)
}

// ReadableWarnings returns detached diagnostics from the existing typed readable allowance plan.
func (r ValidatedExplicitEditResult) ReadableWarnings() []string {
	return append([]string(nil), r.readableWarnings...)
}

// ReadableRequests returns detached statuses produced by the existing readable allowance plan.
func (r ValidatedExplicitEditResult) ReadableRequests() []ValidatedReadableRequest {
	out := make([]ValidatedReadableRequest, len(r.readableRequests))
	for i, request := range r.readableRequests {
		request.Files = append([]string(nil), request.Files...)
		out[i] = request
	}
	return out
}

// ExplicitEditReadResult is an immutable view of one planned graph read. It proves input
// consumption and role binding only; it is NOT a semantic validation result.
type ExplicitEditReadResult struct{ snapshot string }

// SnapshotJSON returns the standard canonical snapshot of the composed effective graph.
func (r ExplicitEditReadResult) SnapshotJSON() string { return r.snapshot }

func resolvedAbsolutePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute: %q", path)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func ValidateExplicitEdit(workspace string, target ExplicitEditTarget, input ConfigReadInput) (ValidatedExplicitEditResult, error) {
	ctx, err := readExplicitEditContext(workspace, target, input)
	if err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	return validateEditContext(ctx, nil)
}

// ValidateProspectiveEdit validates candidate bytes through the standard Project/Local reader
// using a selected-absence receipt; it does not issue an actual-file read witness.
func validateProspectiveEdit(workspace string, target ExplicitEditTarget, input ConfigReadInput, preexisting []string) (ValidatedExplicitEditResult, error) {
	ctx, err := readProspectiveEditContext(workspace, target, input)
	if err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	return validateEditContext(ctx, preexisting)
}

// validateEditContext validates one planned graph. A problem whose problemKey is among
// preexisting's does not refuse it: it is returned in the result's Preexisting instead.
func validateEditContext(ctx *editValidationContext, preexisting []string) (ValidatedExplicitEditResult, error) {
	var err error
	if err := ctx.readRawScopes(); err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	if err := validateExplicitRawReadableScopes(ctx); err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	if err := validateExplicitResourceSources(ctx.userOrigins, "user"); err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	if err := validateExplicitResourceSources(ctx.workspaceOrigins, "workspace"); err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	ctx.packs, err = ctx.selectedPacks()
	if err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	ctx.catalog, err = explicitProviderCatalog(ctx, ctx.effective)
	if err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	errs, warnings := validateConfigWithEditContext(ctx.effective, ctx.workspace, nil, ctx)
	errs, tolerated := splitPreexistingProblems(errs, preexisting)
	if len(errs) != 0 {
		return ValidatedExplicitEditResult{}, &EditProblemsError{Problems: errs}
	}
	if ctx.semantic == nil && len(tolerated) != 0 {
		// The semantic inputs failed on a problem the config already had, so this edit cannot be
		// checked over it: refuse naming that problem, whose text carries its own next step.
		return ValidatedExplicitEditResult{}, &EditProblemsError{Problems: tolerated}
	}
	if ctx.semantic == nil {
		return ValidatedExplicitEditResult{}, fmt.Errorf("validate planned readable F/G semantics: typed consumer did not run")
	}
	if err := ctx.validateSelectedInput(); err != nil {
		return ValidatedExplicitEditResult{}, err
	}
	snapshot, err := SnapshotJSON(ctx.effective)
	if err != nil {
		return ValidatedExplicitEditResult{}, fmt.Errorf("snapshot validated explicit edit config: %w", err)
	}
	readableRequests := make([]ValidatedReadableRequest, 0, len(ctx.semantic.AllowancePlan.Requests))
	for _, request := range ctx.semantic.AllowancePlan.Requests {
		readableRequests = append(readableRequests, ValidatedReadableRequest{
			Path: request.Path, Files: append([]string(nil), request.Files...), Capability: request.Capability,
			Status: request.Status, Reason: request.Reason,
		})
	}
	return ValidatedExplicitEditResult{
		snapshot: snapshot, warnings: append([]string(nil), warnings...),
		readableWarnings: append([]string(nil), ctx.semantic.AllowancePlan.Diagnostics...),
		readableRequests: readableRequests, unverifiedPacks: append([]string(nil), ctx.packNotes...),
		preexisting: tolerated, validated: true,
	}, nil
}

// validateExplicitResourceSources validates each raw resource table before the effective graph
// can mask one source's invalid values with another source's winner.
func validateExplicitResourceSources(sources *Sources, scope string) error {
	if sources == nil || sources.node() == nil {
		return nil
	}
	machine := sources.node().key("machine")
	if machine == nil {
		return nil
	}
	resources := machine.key("resources")
	if resources == nil {
		return nil
	}
	var problems []string
	seen := make(map[string]struct{})
	for _, origin := range resources.writerList() {
		if origin.file == nil {
			continue
		}
		key := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", origin.file.path, origin.host, origin.file.role, origin.file.format, strings.Join(stepPathNames(origin.documentSteps), "."))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		value, err := explicitEditOriginValue(origin)
		if err != nil {
			return fmt.Errorf("read raw %s resource source %s: %w", scope, origin.String(), err)
		}
		sourceConfig := jsonx.NewOrderedMap()
		sourceConfig.Set("resources", value)
		var sourceProblems []string
		validateResources(sourceConfig, &sourceProblems)
		for _, problem := range sourceProblems {
			problem = replaceReadableDiagnosticPrefix(problem, "config.resources", "config.machine.resources")
			fieldOrigin := explicitEditResourceFieldOrigin(origin, problem)
			problem = readableDiagnosticPathForOrigin(problem, fieldOrigin)
			if fieldOrigin.host != "" {
				problem += launchCopyNote([]srcOrigin{fieldOrigin})
			} else {
				problem = fieldOrigin.String() + ": " + problem
			}
			problems = append(problems, problem)
		}
	}
	if len(problems) != 0 {
		return fmt.Errorf("validate raw %s resource config: %s", scope, strings.Join(problems, "; "))
	}
	return nil
}

func explicitEditOriginValue(origin srcOrigin) (any, error) {
	if origin.file == nil {
		return nil, fmt.Errorf("resource source file is missing")
	}
	document := origin.file.document
	if document == nil {
		var err error
		document, err = configdoc.Parse(origin.file.data, origin.file.format)
		if err != nil {
			return nil, err
		}
	}
	var value any = document.Values
	for _, step := range origin.documentSteps {
		if step.IsIndex {
			list, ok := value.([]any)
			if !ok || step.Index < 0 || step.Index >= len(list) {
				return nil, fmt.Errorf("resource source path no longer matches its parsed document")
			}
			value = list[step.Index]
			continue
		}
		object, ok := value.(*jsonx.OrderedMap)
		if !ok {
			return nil, fmt.Errorf("resource source path no longer matches its parsed document")
		}
		child, present := object.Get(step.Key)
		if !present {
			return nil, fmt.Errorf("resource source path %s no longer matches its parsed document", strings.Join(stepPathNames(origin.documentSteps), "."))
		}
		value = child
	}
	return value, nil
}

func explicitEditResourceFieldOrigin(origin srcOrigin, problem string) srcOrigin {
	fieldPath, _, _ := strings.Cut(problem, ":")
	fieldPath = strings.TrimPrefix(fieldPath, "config.resources")
	fieldPath = strings.TrimPrefix(fieldPath, "config.machine.resources")
	fieldPath = strings.TrimPrefix(fieldPath, ".")
	for _, field := range strings.Split(fieldPath, ".") {
		if field == "" {
			continue
		}
		origin.steps = appendStep(origin.steps, json5.Key(field))
		origin.documentSteps = appendDocumentStep(origin.documentSteps, configdoc.Step{Key: field})
	}
	return origin
}

func ReadExplicitEditConfig(workspace string, target ExplicitEditTarget, input ConfigReadInput) (ExplicitEditReadResult, error) {
	ctx, err := readExplicitEditContext(workspace, target, input)
	if err != nil {
		return ExplicitEditReadResult{}, err
	}
	snapshot, err := SnapshotJSON(ctx.effective)
	if err != nil {
		return ExplicitEditReadResult{}, fmt.Errorf("snapshot explicit edit config read: %w", err)
	}
	return ExplicitEditReadResult{snapshot: snapshot}, nil
}

func readExplicitEditContext(workspace string, target ExplicitEditTarget, input ConfigReadInput) (*editValidationContext, error) {
	if target.prospective != nil {
		return nil, fmt.Errorf("prospective selection cannot be consumed as an actual explicit-file witness")
	}
	return readEditContext(workspace, target, input, false)
}

func readProspectiveEditContext(workspace string, target ExplicitEditTarget, input ConfigReadInput) (*editValidationContext, error) {
	if target.prospective == nil || input.prospective == nil || !input.prospective.decodeCandidate {
		return nil, fmt.Errorf("prospective config validation requires a selected-absence target and candidate")
	}
	return readEditContext(workspace, target, input, true)
}

func readEditContext(workspace string, target ExplicitEditTarget, input ConfigReadInput, prospective bool) (*editValidationContext, error) {
	if target.scope == 0 || target.physical == "" || target.workspace == "" || target.format == 0 {
		return nil, fmt.Errorf("explicit edit target is incomplete")
	}
	actualWorkspace, err := resolvedAbsolutePath(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve explicit edit workspace: %w", err)
	}
	if actualWorkspace != target.workspace {
		return nil, fmt.Errorf("explicit edit workspace %s does not match config graph workspace %s", target.workspace, actualWorkspace)
	}
	invocation := input.clone()
	if invocation.target == "" || filepath.Clean(invocation.target) != target.physical {
		return nil, fmt.Errorf("explicit edit input does not match the bound physical target %s", target.physical)
	}
	if inJail() && target.scope == ExplicitEditUser {
		return nil, fmt.Errorf("user-scope config edits are only allowed on the host; run the edit command for %s on the host", target.physical)
	}
	if prospective {
		if target.scope != ExplicitEditProject && target.scope != ExplicitEditLocal || target.format != configdoc.TOML ||
			!sameProspectiveSelection(target.prospective, input.prospective.bound) {
			return nil, fmt.Errorf("prospective config input does not match the bound Project/Local TOML selection")
		}
	} else if input.prospective != nil {
		return nil, fmt.Errorf("actual explicit edit cannot accept a prospective selection receipt")
	}
	witness := &editReadWitness{}
	invocation.witness = witness
	cfg, _, err := composeReadableConfigWithInputUse(actualWorkspace, true, nil, true, true, invocation, nil)
	if err != nil {
		return nil, err
	}
	embeddedFS := packload.EmbeddedFS()
	embeddedNames, embeddedNamesErr := packload.EmbeddedNamesFromFS(embeddedFS)
	ctx := &editValidationContext{
		workspace: actualWorkspace, target: target, input: *invocation, witness: witness,
		embeddedFS: embeddedFS, embeddedNames: embeddedNames, embeddedNamesErr: embeddedNamesErr,
		effective: cfg, reads: append([]editReadObservation(nil), witness.reads...), prospective: prospective,
	}
	if err := ctx.validateSelectedInput(); err != nil {
		return nil, err
	}
	return ctx, nil
}

func validationUserSources(edit ...*editValidationContext) *Sources {
	if len(edit) != 0 && edit[0] != nil {
		return edit[0].userOrigins
	}
	return UserScopeSources()
}

func loadValidationWorkspaceConfigWithNode(workspace string, edit ...*editValidationContext) (*jsonx.OrderedMap, *srcNode, error) {
	if len(edit) != 0 && edit[0] != nil {
		if edit[0].rawWorkspace == nil {
			return nil, nil, fmt.Errorf("planned workspace config source is missing from explicit edit context")
		}
		return edit[0].rawWorkspace, edit[0].workspaceOrigins.node(), nil
	}
	return loadWorkspaceConfig(workspace, false, func(string) {}, true)
}

func loadValidationWorkspaceConfigWithSources(workspace string, edit ...*editValidationContext) (*jsonx.OrderedMap, *Sources, error) {
	if len(edit) != 0 && edit[0] != nil {
		if edit[0].rawWorkspace == nil {
			return nil, nil, fmt.Errorf("planned workspace config source is missing from explicit edit context")
		}
		return edit[0].rawWorkspace, edit[0].workspaceOrigins, nil
	}
	return LoadWorkspaceConfigWithSources(workspace, false, func(string) {})
}

func loadValidationWorkspaceConfig(workspace string, edit ...*editValidationContext) (*jsonx.OrderedMap, error) {
	if len(edit) != 0 && edit[0] != nil {
		if edit[0].rawWorkspace == nil {
			return nil, fmt.Errorf("planned workspace config source is missing from explicit edit context")
		}
		return edit[0].rawWorkspace, nil
	}
	return LoadWorkspaceConfig(workspace, false, func(string) {})
}

func (ctx *editValidationContext) validateSelectedInput() error {
	if ctx == nil {
		return fmt.Errorf("explicit edit context is missing")
	}
	if !ctx.prospective {
		return ctx.validateInputWitness()
	}
	request := ctx.input.prospective
	if request == nil || !request.decodeCandidate || !sameProspectiveSelection(ctx.target.prospective, request.selected) ||
		!sameProspectiveSelection(request.bound, request.selected) {
		return fmt.Errorf("prospective target %s was not selected by the standard Project/Local reader", ctx.target.physical)
	}
	if err := verifyProspectiveSelection(request.selected); err != nil {
		return err
	}
	return nil
}

func (ctx *editValidationContext) validateInputWitness() error {
	if ctx == nil || ctx.witness == nil || len(ctx.witness.reads) == 0 {
		return fmt.Errorf("explicit target %s was not consumed by the standard config graph", ctx.target.physical)
	}
	wantRole := map[ExplicitEditScope]editReadRole{
		ExplicitEditUser:    editRoleUser,
		ExplicitEditProject: editRoleProject,
		ExplicitEditLocal:   editRoleLocal,
	}[ctx.target.scope]
	for _, read := range ctx.witness.reads {
		if read.role != wantRole {
			return fmt.Errorf("explicit target %s was read under conflicting or unauthorized config role %s", ctx.target.physical, read.role.String())
		}
		if read.format != ctx.target.format {
			return fmt.Errorf("explicit target %s was read as a different config format", ctx.target.physical)
		}
	}
	ctx.reads = append([]editReadObservation(nil), ctx.witness.reads...)
	return nil
}

func (ctx *editValidationContext) readRawScopes() error {
	if ctx == nil {
		return fmt.Errorf("explicit edit validation context is missing")
	}
	user, userNode, err := loadReadableUserScopeWithInputUse(paths.UserConfigPath(), true, nil, true, &ctx.input, nil)
	if err != nil {
		return fmt.Errorf("read raw user config scope: %w", err)
	}
	workspace, workspaceNode, err := loadReadableWorkspaceConfigOverWithInputUse(ctx.workspace, true, nil, true, jsonx.NewOrderedMap(), nil, &ctx.input, nil)
	if err != nil {
		return fmt.Errorf("read raw workspace config scope: %w", err)
	}
	ctx.rawUser, ctx.userOrigins = user, sourcesOf(userNode)
	ctx.rawWorkspace, ctx.workspaceOrigins = workspace, sourcesOf(workspaceNode)
	if err := ctx.validateSelectedInput(); err != nil {
		return err
	}
	return nil
}
