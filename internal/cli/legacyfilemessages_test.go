package cli

// legacyfilemessages_test.go enforces LC-D12 of docs/design/legacy-config-compat.md: every
// message that names a config file names the one that exists, and names a verb before a key.
// A message is a string literal in a non-test Go file under internal/ or cmd/, or a chain of
// them joined with `+`, or the arguments of a fmt call. It fails when it:
//
//   - holds a legacy config file name by hand (`config.jsonc`, `yolo-jail.jsonc`,
//     `yolo-jail.local.jsonc`, or their `.json` twins), where paths.UserConfigDisplayPath and
//     config.ResolveReadableWorkspaceConfigPath name the file that exists;
//   - splices in a constant equal to a legacy name (config.WorkspaceConfigName and its kin), or
//     the legacy reader's paths.UserConfigPath(), which answers config.jsonc even when the user's
//     file is config.toml;
//   - spells a value as a JSON snippet to paste (`"packs": [`, `{"kind": "program"`), where the
//     verb that writes it (`yolo pack add`, `yolo config set`, `yolo allow`, `yolo request`)
//     belongs, or, for a pack manifest, the pack.toml spelling.
//
// legacyMessageAllowances is the one exception: a function whose message quotes a legacy input
// it is reading or retiring (a parse error about a legacy-only key, the converter's own report of
// what it converted), or whose literal is a file name it opens and not a message at all. Every
// entry says why, and an entry that no longer matches anything fails the test, so the list only
// shrinks as messages are rewritten.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// legacyConfigFileName is a legacy config file named by hand.
var legacyConfigFileName = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.-])(config\.jsonc|yolo-jail/config\.json|yolo-jail(?:\.local)?\.jsonc?)(?:$|[^A-Za-z0-9_-])`)

// jsonSnippet is a JSON key followed by a JSON value: `"packs": [`, `"enabled": true`,
// `"via": "pack"`, `{"host": %q`. A colon followed by prose (`restart policy "no": not
// restarting`) is not one.
var jsonSnippet = regexp.MustCompile(`"[A-Za-z_][A-Za-z0-9_.-]*": (?:["\[{%…]|true\b|false\b|null\b|[0-9])`)

// legacyNameIdents are the constants equal to a legacy config file name, and the legacy
// reader's path, which a message must not name.
var legacyNameIdents = []string{
	"WorkspaceConfigName", "WorkspaceLocalConfigName", "WorkspaceLegacyConfigName",
	"WorkspaceLocalLegacyConfigName", "UserConfigLegacyJSONCName", "UserConfigLegacyJSONName",
	"WorkspaceConfigLegacyJSONCName", "WorkspaceConfigLegacyJSONName",
	"WorkspaceLocalConfigLegacyJSONCName", "WorkspaceLocalConfigLegacyJSONName", "UserConfigPath",
}

// legacyMessageAllowances is keyed by "<path from the module root>:<enclosing function>" (a
// method is "Type.Method"; a package-level declaration is "<package-level>"), or by a path alone
// ("internal/configmigrate/", "internal/config/brokered.go"), which allows every function under
// it. Two kinds of entry, kept apart: PERMANENT ones, whose literal is a file name yolo opens,
// wire JSON, or a legacy input the message is reading, retiring or converting; and PENDING ones,
// the parts of LC-D12 not yet built (docs/design/legacy-config-compat.md#LC-D12), which are
// deleted as each is rewritten.
var legacyMessageAllowances = map[string]string{
	// PERMANENT: the file names themselves, what the readers open and not messages.
	"internal/paths/paths.go:<package-level>":                             "the legacy file name constants the readers open",
	"internal/cli/run/inheritscope.go:<package-level>":                    "the inherited-layer file names the jail opens",
	"internal/entrypoint/darwinoverlay.go:inheritedCoreConfigDestination": "the core files the home overlay must not refresh, matched by name",

	// PERMANENT: the converter, whose help and reports quote the legacy input it converts.
	"internal/cli/configmigrate.go:<package-level>": "`yolo config migrate`'s help names the old files it converts",
	"internal/configmigrate/":                       "the migration's reports quote the legacy keys (`\"enabled\"`, `brokered`) it converts",

	// PERMANENT: messages about a legacy-only input, quoting the shape that input has.
	"internal/config/brokered.go":                                  "`brokered` is a legacy-only key (RC-D37): its messages quote its legacy shape and the legacy files that hold it",
	"internal/config/workspacefile.go:readWorkspaceFile":           "the per-workspace switch file is JSON yolo writes; its parse errors quote that file",
	"internal/config/validate.go:validateConvertLater":             "`convert_later` is a key only a legacy user file holds",
	"internal/config/validate.go:validateJournalRetired":           "the retirement of a legacy-only `journal` key quotes the value it read",
	"internal/config/validate.go:validateMacosLogRetired":          "the retirement of a legacy-only `macos_log` key quotes the value it read",
	"internal/config/validate.go:validateEnvSources":               "the legacy `env` list's retirement names its legacy successor's shape",
	"internal/config/envsourcescope.go:envSourceScopeRefusal":      "format-aware: the JSON spelling is the legacy-file branch, a TOML file gets the verb",
	"internal/config/mountsrequest.go:writableMountRequestRefusal": "format-aware: the JSON spelling is the legacy-file branch, a TOML file gets the TOML lines",
	"internal/packload/profiles.go:UndeclaredProfileHelp":          "format-aware: the JSON spelling is the legacy-file branch, whose `profiles` key no verb writes",
	"internal/cli/host.go:hostComposition.credentialRemedy":        "format-aware: the JSON spelling is the legacy-file branch, whose `profiles` key no verb writes",
	"internal/cli/pack.go:<package-level>":                         "the pack help shows the legacy spelling of the packs list beside the verb",
	"internal/setupcensus/configkeys.go:<package-level>":           "quotes the user guide's own headings, which the census matches",

	// PERMANENT: wire JSON and documentation of JSON output, not a config value to paste.
	"internal/cli/approve.go:<package-level>":                 "documents `yolo approve --json` output",
	"internal/cli/features.go:<package-level>":                "documents `yolo features --json` output",
	"internal/cli/host.go:<package-level>":                    "pi's own models file, not yolo's config",
	"internal/agentcfg/manifest/load.go:SurfaceDTO.Surface":   "a surface manifest's own JSON field",
	"internal/agentcfg/manifest/manifest.go:NewListCondition": "a surface manifest's own JSON field",
}

// legacyAllowanceFor is the allowance key that covers key: the key itself, or a path entry
// (one with no ":") that is its file or a directory above it.
func legacyAllowanceFor(key string) (string, bool) {
	if _, ok := legacyMessageAllowances[key]; ok {
		return key, true
	}
	for k := range legacyMessageAllowances {
		if strings.Contains(k, ":") {
			continue
		}
		if (strings.HasSuffix(k, "/") && strings.HasPrefix(key, k)) || strings.HasPrefix(key, k+":") {
			return k, true
		}
	}
	return "", false
}

// legacyFinding is one message LC-D12 forbids.
type legacyFinding struct {
	key, pos, what string
}

func TestNoMessageNamesALegacyConfigFileOrASnippet(t *testing.T) {
	root := moduleRootForLegacyScan(t)
	findings := scanLegacyMessages(t, root)
	if len(findings) == 0 {
		t.Fatal("the scan found nothing at all, not even the allowed constants; it has lost its input")
	}
	used := map[string]bool{}
	for _, f := range findings {
		if k, ok := legacyAllowanceFor(f.key); ok {
			used[k] = true
			continue
		}
		t.Errorf("%s (%s): %s. Name the file that exists (paths.UserConfigDisplayPath, "+
			"config.ResolveReadableWorkspaceConfigPath) and the verb that writes the value "+
			"(`yolo config set`, `yolo pack add`, `yolo allow`, `yolo request`), or, for a pack "+
			"manifest, its pack.toml spelling (LC-D12)", f.pos, f.key, f.what)
	}
	keys := make([]string, 0, len(legacyMessageAllowances))
	for k := range legacyMessageAllowances {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !used[k] {
			t.Errorf("legacyMessageAllowances entry %q matches no message any more: delete it", k)
		}
	}
}

// TestTheLegacyMessageScanCatchesEachForm keeps the scan from passing everything.
func TestTheLegacyMessageScanCatchesEachForm(t *testing.T) {
	for _, s := range []string{
		"edit ~/.config/yolo-jail/config.jsonc", "add it to yolo-jail.jsonc", "in yolo-jail.local.json",
		"~/.config/yolo-jail/config.json and relaunch",
	} {
		if !legacyConfigFileName.MatchString(s) {
			t.Errorf("legacyConfigFileName misses %q", s)
		}
	}
	for _, s := range []string{"config.toml", "yolo-jail.toml", "config.jsonc.pre-migrate", "pack.json"} {
		if legacyConfigFileName.MatchString(s) && !strings.HasSuffix(s, ".pre-migrate") {
			t.Errorf("legacyConfigFileName matches %q", s)
		}
	}
	for _, s := range []string{`"packs": ["claude"]`, `{"kind": "program"`, `"enabled": true`, `{"host": %q}`, `"enforce_models": false`} {
		if !jsonSnippet.MatchString(s) {
			t.Errorf("jsonSnippet misses %q", s)
		}
	}
	for _, s := range []string{`restart policy "no": not restarting`, `Failed to load theme "system": Theme not found`, `under "none": there is`} {
		if jsonSnippet.MatchString(s) {
			t.Errorf("jsonSnippet matches prose %q", s)
		}
	}
}

func moduleRootForLegacyScan(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func scanLegacyMessages(t *testing.T, root string) []legacyFinding {
	t.Helper()
	var out []legacyFinding
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			for _, decl := range f.Decls {
				owner := "<package-level>"
				if fd, ok := decl.(*ast.FuncDecl); ok {
					owner = fd.Name.Name
					if fd.Recv != nil && len(fd.Recv.List) == 1 {
						owner = recvTypeName(fd.Recv.List[0].Type) + "." + owner
					}
				}
				key := rel + ":" + owner
				add := func(at token.Pos, what string) {
					out = append(out, legacyFinding{key: key, pos: fmt.Sprintf("%s:%d", rel, fset.Position(at).Line), what: what})
				}
				ast.Inspect(decl, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.BasicLit:
						if n.Kind != token.STRING {
							return true
						}
						v, err := strconv.Unquote(n.Value)
						if err != nil {
							return true
						}
						if m := legacyConfigFileName.FindStringSubmatch(v); m != nil && !strings.Contains(v, m[1]+".pre-migrate") {
							add(n.Pos(), "names the legacy file "+m[1]+" by hand")
						}
						if m := jsonSnippet.FindString(v); m != "" {
							add(n.Pos(), "spells a value as a JSON snippet, "+strconv.Quote(m))
						}
					case *ast.BinaryExpr:
						if n.Op == token.ADD && chainHasString(n) {
							for _, id := range legacyIdentsIn(n) {
								add(n.Pos(), "splices "+id+", a legacy file name, into a message")
							}
						}
					case *ast.CallExpr:
						if isFmtCall(n) {
							// The name may sit inside an argument (l.tilde(paths.UserConfigPath())).
							for _, a := range n.Args {
								ast.Inspect(a, func(x ast.Node) bool {
									if _, ok := x.(*ast.FuncLit); ok {
										return false
									}
									if e, ok := x.(ast.Expr); ok {
										if name := legacyIdent(e); name != "" {
											add(e.Pos(), "formats "+name+", a legacy file name, into a message")
											return false
										}
									}
									return true
								})
							}
						}
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func recvTypeName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(e.X)
	case *ast.IndexExpr:
		return recvTypeName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return "?"
}

// chainHasString reports whether a `+` chain holds a string literal.
func chainHasString(n *ast.BinaryExpr) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if b, ok := x.(*ast.BinaryExpr); ok && b.Op != token.ADD {
			return false
		}
		if l, ok := x.(*ast.BasicLit); ok && l.Kind == token.STRING {
			found = true
		}
		if _, ok := x.(*ast.CallExpr); ok {
			return false
		}
		return true
	})
	return found
}

// legacyIdentsIn is every legacy name a `+` chain splices in directly.
func legacyIdentsIn(n *ast.BinaryExpr) []string {
	var out []string
	for _, side := range []ast.Expr{n.X, n.Y} {
		if b, ok := side.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			continue // the inner chain is visited on its own
		}
		// The name may sit inside the piece (tilde(paths.UserConfigPath())).
		ast.Inspect(side, func(x ast.Node) bool {
			if _, ok := x.(*ast.FuncLit); ok {
				return false
			}
			if e, ok := x.(ast.Expr); ok {
				if name := legacyIdent(e); name != "" {
					out = append(out, name)
					return false
				}
			}
			return true
		})
	}
	return out
}

// legacyIdent names e when it is a legacy file-name constant or a paths.UserConfigPath() call.
func legacyIdent(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		if slices.Contains(legacyNameIdents, e.Name) && e.Name != "UserConfigPath" {
			return e.Name
		}
	case *ast.SelectorExpr:
		if slices.Contains(legacyNameIdents, e.Sel.Name) && e.Sel.Name != "UserConfigPath" {
			return e.Sel.Name
		}
	case *ast.CallExpr:
		if len(e.Args) == 0 {
			switch fn := e.Fun.(type) {
			case *ast.Ident:
				if fn.Name == "UserConfigPath" {
					return "UserConfigPath()"
				}
			case *ast.SelectorExpr:
				if fn.Sel.Name == "UserConfigPath" {
					return "UserConfigPath()"
				}
			}
		}
	}
	return ""
}

// isFmtCall reports a call that writes a message: a fmt formatter, errors.New, or any
// Printf-, Errorf- or WriteString-shaped method (a richtext printer, a strings.Builder).
func isFmtCall(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); ok && (pkg.Name == "fmt" || (pkg.Name == "errors" && sel.Sel.Name == "New")) {
		return true
	}
	switch sel.Sel.Name {
	case "Printf", "Println", "Print", "Errorf", "Warnf", "Sprintf", "Fprintf", "WriteString":
		return true
	}
	return false
}
