package configedit

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ApplyEffective edits one explicit existing file and verifies the result through the standard
// layered config reader. The target is not inferred from diagnostic source locations.
func ApplyEffective(workspace, path string, format configdoc.Format, mutation Mutation, options ApplyOptions) (*Plan, error) {
	return applyEffectiveWithOps(workspace, path, format, mutation, options, systemFileOps)
}

func applyEffectiveWithOps(workspace, path string, format configdoc.Format, mutation Mutation, options ApplyOptions, ops fileOps) (*Plan, error) {
	var plannedSnapshot string
	// preexisting is what the file's config already failed validation for before this edit:
	// it does not refuse the edit, which leaves it as it was (config.EditProblems).
	var preexisting []string
	options.validateTarget = func(physical string, before, planned []byte) error {
		// originalRefusal is the removal's prevalidation finding: a problem the removal resolves
		// (it was in the removed value) refuses it; one it leaves as it was does not.
		var originalRefusal error
		var originalFindings []string
		if mutation.Action == Remove {
			validateOriginal, typeErr := validatesOriginalScalarRemoval(format, mutation.Path, before)
			if typeErr != nil {
				return fmt.Errorf("validate original config %q before removal: %w", physical, typeErr)
			}
			if validateOriginal {
				originalInput, err := config.NewConfigReadInput(physical, before)
				if err != nil {
					return err
				}
				if _, err := config.ValidateExplicitEdit(workspace, options.Target, originalInput); err != nil {
					originalRefusal = fmt.Errorf("validate original config %q before removal: %w", physical, err)
					if originalFindings = config.EditProblems(err); originalFindings == nil {
						return originalRefusal
					}
				}
			}
		}
		input, err := config.NewConfigReadInput(physical, planned)
		if err != nil {
			return err
		}
		result, err := config.ValidateExplicitEdit(workspace, options.Target, input)
		if err != nil && config.EditProblems(err) != nil {
			preexisting = originalProblems(workspace, options.Target, physical, before)
			if preexisting != nil {
				result, err = config.ValidateExplicitEditTolerating(workspace, options.Target, input, preexisting)
			}
		}
		if err != nil {
			return fmt.Errorf("validate planned config: %w", err)
		}
		if originalRefusal != nil && len(config.ResolvedProblems(originalFindings, result.Preexisting())) != 0 {
			return originalRefusal
		}
		validatedSnapshot, ok := result.SnapshotJSON()
		if !ok {
			return fmt.Errorf("planned config validation returned no validated snapshot")
		}
		plannedSnapshot = validatedSnapshot
		if options.OnValidated != nil {
			options.OnValidated(result)
		}
		return nil
	}
	options.verifyTarget = func(physical string, _, after []byte) error {
		input, err := config.NewConfigReadInput(physical, after)
		if err != nil {
			return err
		}
		result, err := config.ValidateExplicitEditTolerating(workspace, options.Target, input, preexisting)
		if err != nil {
			return fmt.Errorf("validate actual config: %w", err)
		}
		actualSnapshot, ok := result.SnapshotJSON()
		if !ok {
			return fmt.Errorf("actual config validation returned no validated snapshot")
		}
		if plannedSnapshot != actualSnapshot {
			return fmt.Errorf("effective config differs from the planned graph")
		}
		return nil
	}
	return applyWithOps(path, format, mutation, options, ops)
}

// originalProblems is the problems the config fails validation for with the file's bytes before
// the edit, nil when it validates or fails for something other than a finding.
func originalProblems(workspace string, target config.ExplicitEditTarget, physical string, before []byte) []string {
	input, err := config.NewConfigReadInput(physical, before)
	if err != nil {
		return nil
	}
	_, err = config.ValidateExplicitEdit(workspace, target, input)
	return config.EditProblems(err)
}

func validatesOriginalScalarRemoval(format configdoc.Format, path []configdoc.Step, data []byte) (bool, error) {
	if format != configdoc.TOML && format != configdoc.JSONC {
		return false, nil
	}
	parts := make([]string, len(path))
	for i, step := range path {
		if step.IsIndex {
			return false, nil
		}
		parts[i] = step.Key
	}
	rules := config.ReadableSchemaKeys()
	if format == configdoc.JSONC {
		if canonical, ok := readableRenamePath(parts); ok {
			parts = canonical
		} else {
			rules = append(rules, config.SchemaKeys()...)
		}
	}
	shape, found := mostSpecificSchemaType(rules, parts)
	if !found || (shape != config.SchemaTypeString && shape != config.SchemaTypeBoolean &&
		shape != config.SchemaTypeNumber && shape != config.SchemaTypeMixed) {
		return false, nil
	}
	doc, err := configdoc.Parse(data, format)
	if err != nil {
		return false, nil
	}
	var value any = doc.Values
	for _, step := range path {
		object, ok := value.(*jsonx.OrderedMap)
		if !ok {
			return true, nil
		}
		var present bool
		value, present = object.Get(step.Key)
		if !present {
			return true, nil
		}
	}
	if value == nil {
		return false, nil
	}
	kind := reflect.ValueOf(value).Kind()
	switch kind {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
	default:
		return false, nil
	}
	if want := scalarSchemaMismatch(shape, value); want != "" {
		return true, fmt.Errorf("config.%s in selected file must be a %s before removal; correct that value manually and run `yolo check`", strings.Join(parts, "."), want)
	}
	return true, nil
}

func scalarSchemaMismatch(shape config.SchemaType, value any) string {
	switch shape {
	case config.SchemaTypeString:
		if _, ok := value.(string); !ok {
			return "string"
		}
	case config.SchemaTypeBoolean:
		if _, ok := value.(bool); !ok {
			return "boolean"
		}
	case config.SchemaTypeNumber:
		if _, ok := jsonx.AsIntLiteral(value); ok {
			return ""
		}
		kind := reflect.ValueOf(value).Kind()
		switch kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return ""
		default:
			return "number"
		}
	}
	return ""
}

func schemaPathMatches(components, path []string) bool {
	if len(components) != len(path) {
		return false
	}
	for i, component := range components {
		if component != "*" && component != path[i] {
			return false
		}
	}
	return true
}

func mostSpecificSchemaType(rules []config.SchemaKey, path []string) (config.SchemaType, bool) {
	bestWildcards := len(path) + 1
	var selected config.SchemaType
	for _, rule := range rules {
		components := strings.Split(rule.Path, ".")
		if len(components) != len(path) {
			continue
		}
		wildcards, matches := 0, true
		for i, component := range components {
			if component == "*" {
				wildcards++
				continue
			}
			if component != path[i] {
				matches = false
				break
			}
		}
		if matches && wildcards < bestWildcards {
			selected = rule.Type
			bestWildcards = wildcards
		}
	}
	return selected, bestWildcards <= len(path)
}

func readableRenamePath(legacy []string) ([]string, bool) {
	var mapped []string
	for _, descriptor := range config.RenameDescriptors() {
		if descriptor.Transform != config.RenameMove || len(descriptor.Replacements) != 1 {
			continue
		}
		old := strings.Split(descriptor.OldPath, ".")
		if len(old) > len(legacy) || !schemaPathMatches(old, legacy[:len(old)]) {
			continue
		}
		replacement := strings.Split(descriptor.Replacements[0], ".")
		captures := make([]string, 0, len(old))
		for i, component := range old {
			if component == "*" {
				captures = append(captures, legacy[i])
			}
		}
		for i, component := range replacement {
			if component == "*" {
				if len(captures) == 0 {
					return nil, false
				}
				replacement[i], captures = captures[0], captures[1:]
			}
		}
		if len(captures) != 0 {
			return nil, false
		}
		candidate := append(replacement, legacy[len(old):]...)
		if mapped != nil && !sameSchemaPath(mapped, candidate) {
			return nil, false
		}
		mapped = candidate
	}
	return mapped, mapped != nil
}

func sameSchemaPath(a, b []string) bool {
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
