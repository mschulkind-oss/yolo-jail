package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
)

func macosUserShellScripts(node ast.Node) []string {
	var scripts []string
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Command" && selector.Sel.Name != "CommandContext") {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "exec" {
			return true
		}
		commandArg := 0
		if selector.Sel.Name == "CommandContext" {
			commandArg = 1
		}
		if commandArg >= len(call.Args) || !isMacosUserShell(call.Args[commandArg]) {
			return true
		}
		for i := commandArg + 1; i < len(call.Args); i++ {
			lit, ok := call.Args[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err == nil && value == "-c" && i+1 < len(call.Args) {
				ast.Inspect(call.Args[i+1], func(part ast.Node) bool {
					lit, ok := part.(*ast.BasicLit)
					if ok && lit.Kind == token.STRING {
						if script, err := strconv.Unquote(lit.Value); err == nil {
							scripts = append(scripts, script)
						}
					}
					return true
				})
				break
			}
		}
		return true
	})
	return scripts
}

func isMacosUserShell(expr ast.Expr) bool {
	name := ""
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			name, _ = strconv.Unquote(e.Value)
		}
	case *ast.Ident:
		name = e.Name
	}
	return name == "sh" || name == "bash" || name == "zsh" || name == "dash" ||
		strings.HasSuffix(name, "/sh") || strings.HasSuffix(name, "/bash") ||
		strings.HasSuffix(name, "/zsh") || strings.HasSuffix(name, "/dash")
}

// rootSudoCommands checks direct command positions in shell text, without parsing general shell syntax.
func rootSudoCommands(script string) []string {
	var commands []string
	var command strings.Builder
	quote := byte(0)
	escaped, comment := false, false
	flush := func() {
		commands = append(commands, command.String())
		command.Reset()
	}
	for i := 0; i < len(script); i++ {
		c := script[i]
		if comment {
			if c != '\n' {
				continue
			}
			comment = false
			flush()
			continue
		}
		if escaped {
			command.WriteByte(c)
			escaped = false
			continue
		}
		if quote != '\'' && c == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			command.WriteByte(c)
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			command.WriteByte(c)
			continue
		}
		if c == '#' && (i == 0 || strings.ContainsRune(" \t\r\n;", rune(script[i-1]))) {
			comment = true
			continue
		}
		if c == ';' || c == '\n' || c == '&' || c == '|' {
			flush()
			if (c == '&' && i+1 < len(script) && script[i+1] == '&') ||
				(c == '|' && i+1 < len(script) && script[i+1] == '|') {
				i++
			}
			continue
		}
		command.WriteByte(c)
	}
	flush()

	var found []string
	for _, text := range commands {
		words := strings.Fields(text)
		for i := 0; i < len(words); i++ {
			word := strings.TrimLeft(words[i], "({")
			if word == "if" || word == "then" || word == "elif" || word == "else" || word == "while" ||
				word == "until" || word == "do" || word == "!" || isShellAssignment(word) {
				continue
			}
			if isSudoCommand(strings.Trim(word, "'\"")) && !isSudoFunctionDeclaration(words, i) &&
				sudoRunsAsRoot(words, i) {
				found = append(found, strings.Trim(word, "'\""))
			}
			break
		}
	}
	return found
}

func isShellAssignment(word string) bool {
	at := strings.IndexByte(word, '=')
	return at > 0 && !strings.ContainsAny(word[:at], "-/")
}

func isSudoCommand(word string) bool {
	return word == "su"+"do" || strings.HasSuffix(word, "/sudo")
}

func isSudoFunctionDeclaration(words []string, i int) bool {
	return i+2 < len(words) && strings.Trim(words[i+1], "()") == "" && words[i+2] == "{"
}

func sudoRunsAsRoot(words []string, i int) bool {
	for i++; i < len(words); i++ {
		arg := strings.Trim(words[i], "'\"")
		if arg == "--" || (len(arg) > 0 && arg[0] != '-') {
			break
		}
		if arg == "-u" || arg == "--user" {
			return i+1 < len(words) && sudoUserIsRoot(words[i+1])
		}
		if strings.HasPrefix(arg, "--user=") {
			return sudoUserIsRoot(strings.TrimPrefix(arg, "--user="))
		}
		if strings.HasPrefix(arg, "-u") && len(arg) > 2 {
			return sudoUserIsRoot(strings.TrimPrefix(strings.TrimPrefix(arg, "-u"), "="))
		}
		switch arg {
		case "-C", "--close-from", "-D", "--chdir", "-g", "--group", "-h", "--host",
			"-p", "--prompt", "-r", "--role", "-R", "--chroot":
			i++
		}
	}
	return true
}

func TestMacosUserSuiteGuardFindsRootSudoInShellCommands(t *testing.T) {
	const source = `package integration
import "os/exec"
func rootCommands() {
 exec.Command("bash", "-c", "printf 'cleanup says sudo rm -rf'; echo ready && sudo -n rm -rf /tmp/fixture; /usr/bin/sudo --user root /bin/true; sudo -n -u root /bin/true; /usr/bin/sudo -n -g wheel --user=_yolojail /usr/bin/env /bin/true")
}
func approvedWrapper() { macosuser.RunAsSandboxArgv("/bin/true") }
func diagnostic() { t.Errorf("cleanup with ` + "`sudo rm -rf /tmp/fixture`" + `") }
`
	f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	scripts := macosUserShellScripts(f)
	if len(scripts) != 1 {
		t.Fatalf("shell scripts extracted = %d, want only the executable -c argument", len(scripts))
	}
	var found []string
	for _, script := range scripts {
		found = append(found, rootSudoCommands(script)...)
	}
	if len(found) != 3 {
		t.Fatalf("root sudo commands = %v, want default-root and explicit -u/--user root commands, not the approved wrapper or diagnostic prose", found)
	}
}

func sudoUserIsRoot(operand string) bool {
	operand = strings.Trim(operand, "'\"")
	return operand == "root" || operand == "#0"
}

func TestMacosUserRootSudoOperandSpellings(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   bool
	}{
		{name: "separated numeric UID zero", script: "sudo -n -u '#0' /bin/true", want: true},
		{name: "separated numeric UID zero long option", script: "sudo --user '#0' /bin/true", want: true},
		{name: "separated numeric UID zero after user", script: "sudo -u '#0' -n /bin/true", want: true},
		{name: "attached numeric UID zero long option", script: "sudo --user='#0' /bin/true", want: true},
		{name: "attached numeric UID zero short option", script: "sudo -u'#0' /bin/true", want: true},
		{name: "attached numeric UID zero short option with equals", script: "sudo -u='#0' /bin/true", want: true},
		{name: "attached single-quoted root long option", script: "sudo --user='root' /bin/true", want: true},
		{name: "attached double-quoted root long option", script: `sudo --user="root" /bin/true`, want: true},
		{name: "attached single-quoted root short option", script: "sudo -u'root' /bin/true", want: true},
		{name: "attached double-quoted root short option", script: `sudo -u"root" /bin/true`, want: true},
		{name: "separated quoted root operand", script: "sudo -n -u 'root' /bin/true", want: true},
		{name: "sandbox user remains allowed", script: "sudo -n --user='_yolojail' /bin/true", want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source := "package integration\nimport \"os/exec\"\nfunc fixture() { exec.Command(\"sh\", \"-c\", " + strconv.Quote(tt.script) + ") }\n"
			f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			scripts := macosUserShellScripts(f)
			if len(scripts) != 1 || scripts[0] != tt.script {
				t.Fatalf("executable shell literals = %q, want [%q]", scripts, tt.script)
			}
			found := rootSudoCommands(scripts[0])
			if got := len(found) == 1; got != tt.want {
				t.Fatalf("root sudo commands = %v, want root detection %t", found, tt.want)
			}
		})
	}
}

func TestMacosUserSuiteRootSudoGuardUsesShellCommandScanner(t *testing.T) {
	_, self, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test source")
	}
	f, err := parser.ParseFile(token.NewFileSet(), self, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var guard *ast.FuncDecl
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "TestMacosUserSuiteSpendsNoRootSudo" {
			guard = fn
			break
		}
	}
	if guard == nil {
		t.Fatal("production no-root-sudo guard was removed")
	}
	called := map[string]bool{}
	ast.Inspect(guard.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if fn, ok := call.Fun.(*ast.Ident); ok {
			called[fn.Name] = true
		}
		return true
	})
	if !called["macosUserShellScripts"] || !called["rootSudoCommands"] {
		t.Fatal("production no-root-sudo guard no longer scans shell command literals")
	}
}

// TestMacosUserSuiteSpendsNoRootSudo is OQ-DM1's guard (docs/design/macos-user-suite-on-a-developer-mac.md,
// ruled 2026-10-18: the suite never needs passwordless root sudo). It reads every Go file in this
// package and fails on any string literal that IS a sudo command name, "sudo" or "/usr/bin/sudo", or
// any root-sudo command found at shell command position inside a literal passed to sh/bash -c.
// The one way a test reaches another account is sandboxCommand (or a production argv builder
// such as macosuser.RunAsSandboxArgv), whose sudo is internal/macosuser's constant and always
// carries `--user=_yolojail` and /usr/bin/env, so a sudo this suite spells itself is one that may
// run as root, or off the rule's shape, and fails on a Mac that grants only what `yolo
// yolo-account create` installs.
//
// Not behind a gate and not -short-gated: it is a reading of the source, so the Linux unit run
// catches the regression before any Mac does. The prefix puts it in the macos-user job too.
// It uses shell command position rather than matching words anywhere, so diagnostic prose remains
// allowed while root-sudo commands embedded in executable scripts are refused.
func TestMacosUserSuiteSpendsNoRootSudo(t *testing.T) {
	// Each entry is file:function, with how many sudo literals it holds and why none of them takes
	// a privilege. The count is exact, so a new sudo in an allowed function still fails.
	type allowance struct {
		count int
		why   string
	}
	allowed := map[string]allowance{
		"macosuserpasswordless_test.go:TestMacosUserPasswordlessStartAndExit": {2, "`sudo -k` drops a " +
			"cached credential, and `sudo -n /usr/bin/true` is the probe that must FAIL: the test " +
			"refuses to run while root sudo needs no password"},
		"macosmachinereset_test.go:runNightlyMachineStep": {1, "the name of a stub written into a " +
			"temporary PATH, so the nightly workflow's script runs no real sudo"},
	}
	_, self, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this package's directory")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(self), "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("listing this package's Go files: %v (%d found)", err, len(files))
	}
	used := map[string]int{}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		base := filepath.Base(path)
		for _, decl := range f.Decls {
			name := "(package scope)"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				name = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v, err := strconv.Unquote(lit.Value)
				// Spelled in halves, so this file's own literals are not what it finds.
				if err != nil || (v != "su"+"do" && v != "/usr/bin/su"+"do") {
					return true
				}
				key := base + ":" + name
				if _, ok := allowed[key]; ok {
					used[key]++
					return true
				}
				t.Errorf("%s spells a sudo command (%s) in %s. The macos-user suite takes no "+
					"privilege `yolo yolo-account create` does not install (OQ-DM1): run a step as the "+
					"sandbox account through sandboxCommand, which builds the one run-as rule's "+
					"argv (macosuser.RunAsSandboxArgv), and every other step as yourself.",
					fset.Position(lit.Pos()), v, name)
				return true
			})
			for _, script := range macosUserShellScripts(decl) {
				for _, command := range rootSudoCommands(script) {
					t.Errorf("%s runs root sudo (%s) in a shell command in %s. The macos-user suite takes no "+
						"privilege `yolo yolo-account create` does not install (OQ-DM1): run a step as the sandbox "+
						"account through sandboxCommand, and every other step as yourself.",
						fset.Position(decl.Pos()), command, name)
				}
			}
		}
	}
	for key, a := range allowed {
		if used[key] != a.count {
			t.Errorf("%s holds %d sudo literal(s) and its allowance admits exactly %d (%s). A new one "+
				"there is a new privilege: route it through sandboxCommand. One gone: lower the count, "+
				"or remove the allowance, so it cannot admit a new one later", key, used[key], a.count, a.why)
		}
	}
}
