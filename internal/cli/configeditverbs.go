package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/check"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func configEditWorkspace() string {
	if config.InJail() {
		return config.JailWorkspace()
	}
	if workspace, ok := resolveWorkspaceRoot(); ok {
		return workspace
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

func runConfigEditWith(args []string, out, errw io.Writer, workspace string) int {
	return runConfigEditWithMode(args, out, errw, workspace, false)
}

func runConfigEditWithPostWriteCheck(args []string, out, errw io.Writer, workspace string) int {
	return runConfigEditWithMode(args, out, errw, workspace, true)
}

func runConfigEditWithMode(args []string, out, errw io.Writer, workspace string, postWriteCheck bool) int {
	if len(args) == 0 {
		fmt.Fprintln(errw, "usage: yolo config set|add|unset <key> [value] [--user|--project|--local|--file PATH] [--dry-run]")
		return 2
	}
	actionName := args[0]
	var action configedit.Action
	switch actionName {
	case "set":
		action = configedit.Set
	case "add":
		action = configedit.Append
	case "unset":
		action = configedit.Remove
	default:
		fmt.Fprintf(errw, "unknown config edit verb %q\n", actionName)
		return 2
	}

	choice := config.EditTargetChoice{Kind: config.EditTargetAutomatic}
	dryRun := false
	positionals := make([]string, 0, 2)
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--user", "--project", "--local":
			if choice.Kind != config.EditTargetAutomatic {
				fmt.Fprintln(errw, "choose only one of --user, --project, --local, or --file")
				return 2
			}
			switch args[i] {
			case "--user":
				choice.Kind = config.EditTargetUser
			case "--project":
				choice.Kind = config.EditTargetProject
			case "--local":
				choice.Kind = config.EditTargetLocal
			}
		case "--file":
			if choice.Kind != config.EditTargetAutomatic || i+1 == len(args) {
				fmt.Fprintln(errw, "--file requires one path and cannot be combined with another target flag")
				return 2
			}
			choice = config.EditTargetChoice{Kind: config.EditTargetFile, File: args[i+1]}
			i++
		case "--dry-run":
			dryRun = true
		default:
			if strings.HasPrefix(args[i], "--") {
				fmt.Fprintf(errw, "unknown config edit flag %q\n", args[i])
				return 2
			}
			positionals = append(positionals, args[i])
		}
	}
	wantPositionals := 1
	if action != configedit.Remove {
		wantPositionals = 2
	}
	if len(positionals) != wantPositionals {
		fmt.Fprintf(errw, "%s requires a key", actionName)
		if action != configedit.Remove {
			fmt.Fprint(errw, " and one value")
		}
		fmt.Fprintln(errw)
		return 2
	}

	key, err := parseConfigEditKey(positionals[0])
	if err != nil {
		fmt.Fprintf(errw, "invalid config key: %v\n", err)
		return 2
	}
	// RC-D21: no command writes convert_later; the person who wants the conversion offer to stop
	// writes it by hand, once, in the old user file.
	if action != configedit.Remove && len(key) == 1 && !key[0].IsIndex && key[0].Key == config.ConvertLaterKey {
		fmt.Fprintf(errw, "yolo config %s: %s is written by hand, never by a command; nothing was written. "+
			"To stop the launch offering the conversion, add \"%s\": true to %s yourself; "+
			"to convert instead, run `yolo config migrate`\n",
			actionName, config.ConvertLaterKey, config.ConvertLaterKey, paths.UserConfigDisplayPath())
		return 1
	}
	mutation := configedit.Mutation{Action: action, Path: key}
	if configEditNeedsHostForJailedUserScope(key, choice) {
		printConfigEditHostRun(errw, workspace, args)
		return 1
	}
	if action != configedit.Remove {
		// Target discovery gives the actual file format before interpreting a typed value.
		target, resolveErr := configedit.ResolveTarget(workspace, mutation, choice)
		if resolveErr != nil {
			fmt.Fprint(errw, configEditRefusal(resolveErr))
			return 1
		}
		if config.InJail() && target.Role == config.ExplicitEditUser {
			printConfigEditHostRun(errw, workspace, args)
			return 1
		}
		valuePath := key
		if action == configedit.Append {
			valuePath = append(append([]configdoc.Step(nil), key...), configdoc.Step{Key: "*"})
		}
		value, valueErr := parseConfigEditValue(positionals[1], valuePath)
		if valueErr != nil {
			fmt.Fprintf(errw, "invalid config value: %v\n", valueErr)
			return 1
		}
		mutation.Value = value
		// OQ-RC10: the generic verbs work for ANY key, so a key the selected file does not hold
		// yet is added, with its missing parent tables, as the named verbs already do.
		mutation, target, ok := addMissingKey(workspace, target, mutation, value, choice, errw)
		if !ok {
			return 1
		}
		return applyConfigEdit(workspace, target, mutation, dryRun, out, errw, postWriteCheck)
	}

	if config.InJail() && choice.Kind == config.EditTargetUser {
		printConfigEditHostRun(errw, workspace, args)
		return 1
	}
	target, err := configedit.ResolveTarget(workspace, mutation, choice)
	if err != nil {
		fmt.Fprint(errw, configEditRefusal(err))
		return 1
	}
	if config.InJail() && target.Role == config.ExplicitEditUser {
		printConfigEditHostRun(errw, workspace, args)
		return 1
	}
	mutation, err = configEditUnsetMutation(target.Format, key, target.AuthoredPath(), target.ExpectedBytes())
	if err != nil {
		fmt.Fprintf(errw, "cannot unset config key %q in selected file %q: %v; edit the value manually in that file, then run `yolo check` in this workspace", positionals[0], target.LogicalPath, err)
		if target.Role != config.ExplicitEditUser {
			fmt.Fprint(errw, "; run `yolo config apply` on the host to approve workspace changes")
		}
		fmt.Fprintln(errw)
		return 1
	}
	mutation.Path = append([]configdoc.Step(nil), key...)
	if !configEditPathPresent(target.ExpectedBytes(), target.Format, target.AuthoredPath()) {
		effective, loadErr := config.LoadReadableConfig(workspace, true, nil)
		if loadErr != nil {
			fmt.Fprintf(errw, "cannot determine whether config key is already absent: %v\n", loadErr)
			return 1
		}
		if !configEditValuePresent(effective, key) {
			mutation = configedit.Mutation{Action: configedit.Remove, Path: append([]configdoc.Step(nil), key...)}
		}
	}
	// Resolve again with the effective authored spelling. The target identity is unchanged,
	// while the mutation intent is checked by ApplyTargeted before any write.
	resolved, err := configedit.ResolveTarget(workspace, mutation, choice)
	if err != nil {
		fmt.Fprint(errw, configEditRefusal(err))
		return 1
	}
	if config.InJail() && resolved.Role == config.ExplicitEditUser {
		printConfigEditHostRun(errw, workspace, args)
		return 1
	}
	return applyConfigEdit(workspace, resolved, mutation, dryRun, out, errw, postWriteCheck)
}

// addMissingKey turns a set or an add (append) on a key the selected file does not hold into an
// Add of that key (a list holding the one item for an append), re-resolving the target for the
// new intent. A key present in the file, or one ending in an array index, is returned untouched.
func addMissingKey(workspace string, target config.ResolvedEditTarget, mutation configedit.Mutation, value any,
	choice config.EditTargetChoice, errw io.Writer) (configedit.Mutation, config.ResolvedEditTarget, bool) {
	last := mutation.Path[len(mutation.Path)-1]
	if last.IsIndex || configEditPathPresent(target.ExpectedBytes(), target.Format, target.AuthoredPath()) {
		return mutation, target, true
	}
	whenMissing := value
	if mutation.Action == configedit.Append {
		whenMissing = []any{value}
	}
	added := configedit.Mutation{Action: configedit.Add, Path: mutation.Path, Value: whenMissing}
	resolved, err := configedit.ResolveTarget(workspace, added, choice)
	if err != nil {
		fmt.Fprint(errw, configEditRefusal(err))
		return mutation, target, false
	}
	return added, resolved, true
}

func applyConfigEdit(workspace string, target config.ResolvedEditTarget, mutation configedit.Mutation, dryRun bool, out, errw io.Writer, postWriteCheck bool) int {
	// A pack the jail could not read went unchecked; say so once, however many times the edit
	// validated (planned, then actual), so the edit never reads as a full check.
	var unverified []string
	onValidated := func(r config.ValidatedExplicitEditResult) {
		if unverified == nil {
			unverified = r.UnverifiedPacks()
		}
	}
	result, err := configedit.ApplyTargeted(workspace, target, mutation, configedit.ApplyOptions{DryRun: dryRun, OnValidated: onValidated})
	if err != nil {
		fmt.Fprintf(errw, "config edit refused: %v\n", err)
		return 1
	}
	for _, note := range unverified {
		fmt.Fprintf(errw, "note: %s\n", note)
	}
	writeConfigEditPreexisting(errw, result)
	if result.Plan.Noop {
		fmt.Fprintf(out, "%s\nNo change; no write.\n", result.Path)
		return 0
	}
	hunks := redactConfigEditHunks(result.Plan.Hunks, target.ExpectedBytes(), target.Format, mutation)
	if dryRun {
		fmt.Fprintf(out, "%s\n", result.Path)
		writeConfigEditHunks(out, hunks)
		fmt.Fprintln(out, "Dry run; nothing was written.")
		return 0
	}
	fmt.Fprintf(out, "%s\n", result.Path)
	writeConfigEditHunks(out, hunks)
	if target.Role == config.ExplicitEditUser {
		fmt.Fprintln(out, "Written.")
	} else {
		fmt.Fprintln(out, projectWrittenLine(postWriteCheck))
	}
	if postWriteCheck {
		checkRC := runConfigPostWriteCheck(workspace, out, errw)
		if checkRC != 0 {
			fmt.Fprintln(errw, "The config file was written, but the post-write check did not pass; follow its findings before continuing.")
			return checkRC
		}
	}
	return 0
}

// projectWrittenLine is the line after a project edit. When the check runs right after it,
// the line does not ask the reader to run that check again.
func projectWrittenLine(postWriteCheck bool) string {
	if postWriteCheck {
		return "Written. The host must run `yolo config apply` to approve the workspace change."
	}
	return "Written. Run `yolo check` in this workspace; the host must run `yolo config apply` to approve the workspace change."
}

func runConfigPostWriteCheck(workspace string, out, errw io.Writer) int {
	opts, ok := checkOptions([]string{"check", "--no-build"}, errw)
	if !ok {
		return 2
	}
	opts.Workspace = workspace
	opts.Stdout = out
	opts.Stderr = errw
	opts.Color = colorForWriter(out)
	return check.Check(opts)
}

// writeConfigEditPreexisting warns, one line each, of a problem the config already had, which the
// edit left as it was and so was not refused for. Each problem names where it is written and its fix.
func writeConfigEditPreexisting(errw io.Writer, result configedit.TargetedResult) {
	for _, problem := range result.Preexisting {
		fmt.Fprintf(errw, "warning: already in your config, and left as it is by this edit: %s\n", problem)
	}
}

func redactConfigEditHunks(hunks []configedit.Hunk, before []byte, format configdoc.Format,
	mutation configedit.Mutation) []configedit.Hunk {
	values := privateEnvValues(before, format)
	if privateEnvMutation(mutation.Path) {
		values = append(values, nestedPrivateEnvMutationValues(mutation.Path, mutation.Value)...)
	}
	if len(values) == 0 {
		return hunks
	}
	// Longer spellings first, so a quoted value is replaced as a unit before its raw content.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	out := append([]configedit.Hunk(nil), hunks...)
	for i := range out {
		for _, value := range values {
			if value == "" {
				continue
			}
			out[i].Before = bytes.ReplaceAll(out[i].Before, []byte(value), []byte(`"<redacted>"`))
			out[i].After = bytes.ReplaceAll(out[i].After, []byte(value), []byte(`"<redacted>"`))
		}
	}
	return out
}

func privateEnvValues(data []byte, format configdoc.Format) []string {
	if format != configdoc.TOML {
		return nil
	}
	doc, err := configdoc.Parse(data, format)
	if err != nil {
		return nil
	}
	envValue, ok := doc.Values.Get("env")
	if !ok {
		return nil
	}
	env, ok := envValue.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	secretsValue, ok := env.Get("secrets")
	if !ok {
		return nil
	}
	secrets, ok := secretsValue.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	setValue, ok := secrets.Get("set")
	if !ok {
		return nil
	}
	set, ok := setValue.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	var values []string
	for _, name := range set.Keys() {
		value, ok := set.Get(name)
		if !ok {
			continue
		}
		span, found, err := doc.Index.Value(configdoc.Step{Key: "env"}, configdoc.Step{Key: "secrets"},
			configdoc.Step{Key: "set"}, configdoc.Step{Key: name})
		if err != nil || !found || span.Start < 0 || span.End > len(data) || span.Start >= span.End {
			continue
		}
		values = append(values, string(data[span.Start:span.End]))
		if text, ok := value.(string); ok {
			values = append(values, configEditStringSpellings(text)...)
		}
	}
	return values
}

func privateEnvMutation(path []configdoc.Step) bool {
	if len(path) >= 3 && path[0].Key == "env" && path[1].Key == "secrets" && path[2].Key == "set" {
		return true
	}
	return len(path) == 2 && path[0].Key == "env" && path[1].Key == "secrets"
}

func nestedPrivateEnvMutationValues(path []configdoc.Step, value any) []string {
	if len(path) >= 3 && path[0].Key == "env" && path[1].Key == "secrets" && path[2].Key == "set" {
		return configEditValueSpellings(value)
	}
	if len(path) == 2 && path[0].Key == "env" && path[1].Key == "secrets" {
		secrets, ok := value.(*jsonx.OrderedMap)
		if !ok {
			return nil
		}
		set, ok := secrets.Get("set")
		if !ok {
			return nil
		}
		return configEditValueSpellings(set)
	}
	return nil
}

func configEditValueSpellings(value any) []string {
	switch v := value.(type) {
	case string:
		return configEditStringSpellings(v)
	case *jsonx.OrderedMap:
		var out []string
		for _, key := range v.Keys() {
			item, _ := v.Get(key)
			out = append(out, configEditValueSpellings(item)...)
		}
		return out
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, configEditValueSpellings(item)...)
		}
		return out
	default:
		return nil
	}
}

func configEditStringSpellings(value string) []string {
	if value == "" {
		return []string{`""`, `''`}
	}
	out := []string{strconv.Quote(value)}
	if !strings.ContainsAny(value, "'\n\r") {
		out = append(out, "'"+value+"'")
	}
	return out
}

func writeConfigEditHunks(out io.Writer, hunks []configedit.Hunk) {
	for _, hunk := range hunks {
		fmt.Fprintln(out, "@@")
		if len(hunk.Before) > 0 {
			fmt.Fprintf(out, "-%s", hunk.Before)
			if hunk.Before[len(hunk.Before)-1] != '\n' {
				fmt.Fprintln(out)
			}
		}
		if len(hunk.After) > 0 {
			fmt.Fprintf(out, "+%s", hunk.After)
			if hunk.After[len(hunk.After)-1] != '\n' {
				fmt.Fprintln(out)
			}
		}
	}
}

func configEditNeedsHostForJailedUserScope(path []configdoc.Step, choice config.EditTargetChoice) bool {
	if !config.InJail() {
		return false
	}
	rule, ok := configEditSchemaRule(path, configdoc.TOML)
	if !ok {
		return false
	}
	if rule.Scope == config.SchemaScopeWorkspaceOnly {
		return false
	}
	if choice.Kind == config.EditTargetFile && configEditIsCanonicalUserFile(choice.File) {
		return true
	}
	return choice.Kind == config.EditTargetUser || (choice.Kind == config.EditTargetAutomatic && rule.Scope == config.SchemaScopeUserOnly)
}

func configEditIsCanonicalUserFile(file string) bool {
	absolute, err := filepath.Abs(file)
	if err != nil {
		return false
	}
	userDir := filepath.Dir(paths.UserConfigPath())
	for _, name := range []string{paths.UserConfigTOMLName, paths.UserConfigLegacyJSONCName, paths.UserConfigLegacyJSONName} {
		if filepath.Clean(absolute) == filepath.Join(userDir, name) {
			return true
		}
	}
	return false
}

func printConfigEditHostRun(errw io.Writer, workspace string, argv []string) {
	command := shquote.Join(append([]string{"yolo", "config"}, argv...))
	fmt.Fprintf(errw, "user-scope config edits must run on the host; run: yolo host-run -C %s --name config-edit -- %s\n",
		shquote.QuoteDisplay(workspace), command)
}

func configEditPathPresent(data []byte, format configdoc.Format, path []configdoc.Step) bool {
	doc, err := configdoc.Parse(data, format)
	if err != nil {
		return false
	}
	_, found, err := doc.Index.Value(path...)
	return err == nil && found
}

func configEditValuePresent(root any, path []configdoc.Step) bool {
	value := root
	for _, step := range path {
		if step.IsIndex {
			items, ok := value.([]any)
			if !ok || step.Index < 0 || step.Index >= len(items) {
				return false
			}
			value = items[step.Index]
			continue
		}
		object, ok := value.(*jsonx.OrderedMap)
		if !ok {
			return false
		}
		var present bool
		value, present = object.Get(step.Key)
		if !present {
			return false
		}
	}
	return true
}

func configEditUnsetMutation(format configdoc.Format, canonicalPath, authoredPath []configdoc.Step, data []byte) (configedit.Mutation, error) {
	// A KEY NAMED SETUPS REPLACE (config.RemovedSetupKeys) is in no schema any more, yet every
	// refusal of one names this unset as its step (named-setups-plan.md SUP-D17): it removes
	// the line, in JSONC and TOML alike.
	if len(authoredPath) == 1 && !authoredPath[0].IsIndex && config.IsRemovedSetupKey(authoredPath[0].Key) {
		return configedit.Mutation{Action: configedit.Remove, Path: append([]configdoc.Step(nil), authoredPath...)}, nil
	}
	rule, ok := configEditSchemaRule(authoredPath, format)
	if !ok && format == configdoc.JSONC {
		rule, ok = configEditSchemaRule(canonicalPath, configdoc.TOML)
	}
	if !ok {
		return configedit.Mutation{}, fmt.Errorf("key has no removal policy in the selected config schema")
	}
	// Removal spelling follows the authored schema; scalar shape follows the canonical key.
	if rule.Removal == config.RemovalUnspecified {
		if canonicalRule, canonicalOK := configEditSchemaRule(canonicalPath, configdoc.TOML); canonicalOK {
			rule.Type = canonicalRule.Type
		}
	}
	mutation := configedit.Mutation{Action: configedit.Set, Path: append([]configdoc.Step(nil), authoredPath...)}
	// A plain scalar entry with an "off" spelling (a mise tool's version, RC-D52) is unset by
	// removing this file's line, so a lower file's version shows through, as before the
	// spelling existed; "off", which drops the lower one too, is `yolo config set ... off`.
	if rule.Removal == config.RemovalOffString && rule.Type == config.SchemaTypeString {
		rule.Removal = config.RemovalUnspecified
	}
	switch rule.Removal {
	case config.RemovalNull, config.RemovalTombstone:
		if format != configdoc.JSONC {
			return configedit.Mutation{}, fmt.Errorf("the selected TOML schema does not define a null removal")
		}
		mutation.Value = nil
	case config.RemovalEmptyList:
		if format != configdoc.TOML {
			return configedit.Mutation{}, fmt.Errorf("the selected legacy schema does not define empty-list removal here")
		}
		mutation.Value = []any{}
	case config.RemovalOffString:
		if format != configdoc.TOML {
			return configedit.Mutation{}, fmt.Errorf("the selected legacy schema does not define off-string removal here")
		}
		mutation.Value = "off"
	case config.RemovalNativeString:
		if format != configdoc.TOML {
			return configedit.Mutation{}, fmt.Errorf("the selected legacy schema does not define native-string removal here")
		}
		mutation.Value = "native"
	case config.RemovalUnsetList:
		return configedit.Mutation{}, fmt.Errorf("this key's removal requires updating both env.set and env.unset; no multi-key edit is available")
	case config.RemovalUnspecified:
		// A pack's setting, `allow.<pack>.<setting>`, is the pack's own to type (Any here), and
		// its whole value is this file's line: unset removes it, list or table, so the advice
		// that names one (a sets list, a profile) has a verb that carries it out.
		if format == configdoc.TOML && isAllowPackSetting(authoredPath) {
			mutation.Action = configedit.Remove
			return mutation, nil
		}
		if rule.Type == config.SchemaTypeAny || rule.Type == config.SchemaTypeObject ||
			rule.Type == config.SchemaTypeList || rule.Type == config.SchemaTypeObjectOrList {
			return configedit.Mutation{}, fmt.Errorf("this key has no scalar removal policy in the selected config schema")
		}
		value, present, err := configEditAuthoredValue(data, format, authoredPath)
		if err != nil {
			return configedit.Mutation{}, err
		}
		if present && !configEditIsScalar(value) {
			return configedit.Mutation{}, fmt.Errorf("this key's authored value is not an ordinary scalar and has no removal policy")
		}
		mutation.Action = configedit.Remove
	default:
		return configedit.Mutation{}, fmt.Errorf("the selected config schema does not define a removal for this key")
	}
	return mutation, nil
}

// isAllowPackSetting reports whether path is `allow.<pack>.<setting>`: exactly three keys, the
// first `allow`, none an index.
func isAllowPackSetting(path []configdoc.Step) bool {
	if len(path) != 3 || path[0].Key != "allow" {
		return false
	}
	for _, step := range path {
		if step.IsIndex || step.Key == "" {
			return false
		}
	}
	return true
}

func configEditAuthoredValue(data []byte, format configdoc.Format, path []configdoc.Step) (any, bool, error) {
	doc, err := configdoc.Parse(data, format)
	if err != nil {
		return nil, false, fmt.Errorf("cannot inspect authored config value: %w", err)
	}
	var value any = doc.Values
	for _, step := range path {
		if step.IsIndex {
			items, ok := value.([]any)
			if !ok || step.Index < 0 || step.Index >= len(items) {
				return nil, false, nil
			}
			value = items[step.Index]
			continue
		}
		object, ok := value.(*jsonx.OrderedMap)
		if !ok {
			return nil, false, nil
		}
		var present bool
		value, present = object.Get(step.Key)
		if !present {
			return nil, false, nil
		}
	}
	return value, true, nil
}

func configEditIsScalar(value any) bool {
	if value == nil {
		return false
	}
	kind := reflect.ValueOf(value).Kind()
	switch kind {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func parseConfigEditValue(raw string, path []configdoc.Step) (any, error) {
	rule, ok := configEditSchemaRule(path, configdoc.TOML)
	if !ok {
		return nil, fmt.Errorf("key has no value type in the readable config schema")
	}
	if rule.Type == config.SchemaTypeString && !looksLikeTOMLString(raw) {
		return raw, nil
	}
	value, err := parseTOMLConfigValue(raw)
	if err != nil {
		// A mixed element (a package name that may also be a table) accepts a bare word as a string.
		// So does a pack's setting (Any here), so `yolo config set allow.aws-auth.profile sso`,
		// the form every advice line names, writes the profile rather than a TOML error.
		if (rule.Type == config.SchemaTypeMixed || (rule.Type == config.SchemaTypeAny && isAllowPackSetting(path))) &&
			!strings.ContainsAny(raw, "\"'[]{}=#, \t\n") {
			return raw, nil
		}
		return nil, err
	}
	if rule.Type == config.SchemaTypeString {
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("expected a string; quote it as a TOML string if needed")
		}
	}
	return value, nil
}

func looksLikeTOMLString(raw string) bool {
	return strings.HasPrefix(raw, "\"") || strings.HasPrefix(raw, "'")
}

func parseTOMLConfigValue(raw string) (any, error) {
	doc, err := configdoc.Parse([]byte("value = "+raw+"\n"), configdoc.TOML)
	if err != nil {
		return nil, fmt.Errorf("expected a TOML value: %w", err)
	}
	keys := doc.Values.Keys()
	if len(keys) != 1 || keys[0] != "value" {
		return nil, fmt.Errorf("expected one TOML value; additional top-level keys are not accepted")
	}
	value, ok := doc.Values.Get("value")
	if !ok {
		return nil, fmt.Errorf("TOML value was not parsed")
	}
	return value, nil
}

func configEditSchemaRule(path []configdoc.Step, format configdoc.Format) (config.SchemaKey, bool) {
	parts := make([]string, len(path))
	for i, step := range path {
		if step.IsIndex {
			parts[i] = "*"
		} else {
			parts[i] = step.Key
		}
	}
	var rules []config.SchemaKey
	if format == configdoc.TOML {
		rules = config.ReadableSchemaKeys()
	} else {
		rules = config.SchemaKeys()
	}
	bestWildcards := len(parts) + 1
	var selected config.SchemaKey
	for _, candidate := range rules {
		candidateParts := strings.Split(candidate.Path, ".")
		if len(candidateParts) != len(parts) {
			continue
		}
		wildcards, matches := 0, true
		for i, component := range candidateParts {
			if component == "*" {
				wildcards++
				continue
			}
			if component != parts[i] {
				matches = false
				break
			}
		}
		if matches && wildcards < bestWildcards {
			selected = candidate
			bestWildcards = wildcards
		}
	}
	return selected, bestWildcards <= len(parts)
}

func parseConfigEditKey(raw string) ([]configdoc.Step, error) {
	var steps []configdoc.Step
	for i := 0; i < len(raw); {
		if raw[i] == '.' {
			return nil, fmt.Errorf("empty key component")
		}
		if raw[i] == '[' {
			i++
			start := i
			for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
				i++
			}
			if start == i || i >= len(raw) || raw[i] != ']' {
				return nil, fmt.Errorf("array indexes must be non-negative integers in brackets")
			}
			index, err := strconv.Atoi(raw[start:i])
			if err != nil {
				return nil, err
			}
			i++
			steps = append(steps, configdoc.Step{Index: index, IsIndex: true})
		} else {
			component, next, err := parseConfigKeyComponent(raw, i)
			if err != nil {
				return nil, err
			}
			i = next
			steps = append(steps, configdoc.Step{Key: component})
		}
		if i == len(raw) {
			break
		}
		if raw[i] == '.' {
			i++
			if i == len(raw) {
				return nil, fmt.Errorf("empty key component")
			}
			continue
		}
		if raw[i] != '[' {
			return nil, fmt.Errorf("expected dot or array index after byte %d", i)
		}
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("key is empty")
	}
	return steps, nil
}

func parseConfigKeyComponent(raw string, start int) (string, int, error) {
	if raw[start] == '"' {
		for i := start + 1; i < len(raw); i++ {
			if raw[i] == '\\' {
				i++
				continue
			}
			if raw[i] == '"' {
				value, err := strconv.Unquote(raw[start : i+1])
				return value, i + 1, err
			}
		}
		return "", start, fmt.Errorf("unterminated quoted key component")
	}
	if raw[start] == '\'' {
		end := strings.IndexByte(raw[start+1:], '\'')
		if end < 0 {
			return "", start, fmt.Errorf("unterminated literal key component")
		}
		end += start + 1
		return raw[start+1 : end], end + 1, nil
	}
	end := start
	for end < len(raw) && raw[end] != '.' && raw[end] != '[' {
		if raw[end] == ']' || raw[end] == ' ' || raw[end] == '\t' || raw[end] == '\n' {
			return "", start, fmt.Errorf("invalid byte in unquoted key component")
		}
		end++
	}
	if end == start {
		return "", start, fmt.Errorf("empty key component")
	}
	return raw[start:end], end, nil
}

// configEditRefusal words a target-resolution failure. A config the graph cannot read (a value of
// the wrong shape, such as a scalar where a table is needed) gets its next step: the edit verbs
// cannot repair a file they cannot read.
func configEditRefusal(err error) string {
	msg := fmt.Sprintf("cannot edit config: %v", err)
	if strings.Contains(err.Error(), "read config graph for edit target") {
		msg += "; fix the value named above in that file by hand, then run `yolo check` and repeat the command"
	}
	return msg + "\n"
}
