package cli

// valueflags_test.go pins docs/plans/notch-convergence.md item 9 (rows A1, A2): one value-flag
// reader for `-p`/`--profile`, `--at`, `--network` and `--with-credentials`, so the same
// spelling means the same thing at `yolo [run]`, `yolo host --` and `yolo host env`. Done when
// `--profile=` refuses with rc 2 at both notches and `-p=zai` works at both.

import (
	"bytes"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// valueFlagHome points HOME and the XDG roots at a scratch directory, with userConfig as the
// user-scope config, and runs the test from an empty workspace whose config does not parse. So
// a jail launch that got past the refusal under test fails at config load, naming it, and
// never reaches a container; and nothing is read from or written to the real home.
func valueFlagHome(t *testing.T, userConfig string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("YOLO_VERSION", "")
	if userConfig != "" {
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), userConfig)
	}
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "yolo-jail.jsonc"), "{ this is not json")
	t.Chdir(ws)
}

// A MISSING OR EMPTY VALUE REFUSES WITH RC 2 AT EVERY NOTCH, in one sentence. Driven through
// the entry points a user reaches — runRun for the jail, hostMain for `yolo host --` and
// `yolo host env` — so deleting the refusal at any one call site fails its row: the jail
// falls through to the config load (and says "Failed to parse"), the host to a launch.
func TestValueFlagWithNoValueIsRefused(t *testing.T) {
	valueFlagHome(t, "")
	orig := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error {
		t.Error("a launch given a value flag with no value reached the exec")
		return nil
	}
	t.Cleanup(func() { hostSyscallExec = orig })

	for _, tc := range []struct {
		flags []string
		name  string
	}{
		{[]string{"--profile="}, "--profile"},
		{[]string{"-p="}, "-p"},
		{[]string{"-p"}, "-p"},
		{[]string{"--profile"}, "--profile"},
		{[]string{"--network="}, "--network"},
		{[]string{"--at="}, "--at"},
	} {
		want := tc.name + " needs a value"
		// The jail: `yolo <flags> -- claude`, through the front door's rewrite.
		var rc int
		_, errs := captureBoth(t, func() { rc = runRun(RewriteArgv(append(tc.flags, "--", "claude"))) })
		if rc != 2 || !strings.Contains(errs, "yolo run: "+want) {
			t.Errorf("jail `yolo %s -- claude`: rc=%d, want 2 and %q\n%s",
				strings.Join(tc.flags, " "), rc, "yolo run: "+want, errs)
		}
		if strings.Contains(errs, "Failed to parse") {
			t.Errorf("jail `yolo %s -- claude` reached the launch pipeline", strings.Join(tc.flags, " "))
		}
		if tc.name == "--network" || tc.name == "--at" {
			continue // host rows for these belong to the notch front door (item 10)
		}
		// The host exec.
		var out, errw bytes.Buffer
		if rc := hostMain(append(append([]string{}, tc.flags...), "--", "true"), &out, &errw, false, nil); rc != 2 ||
			!strings.Contains(errw.String(), "yolo host: "+want) {
			t.Errorf("host `yolo host %s -- true`: rc=%d, want 2 and %q\n%s",
				strings.Join(tc.flags, " "), rc, "yolo host: "+want, errw.String())
		}
		// The host's shell front door.
		out.Reset()
		errw.Reset()
		if rc := hostMain(append([]string{"env"}, tc.flags...), &out, &errw, false, nil); rc != 2 ||
			!strings.Contains(errw.String(), "yolo host env: "+want) {
			t.Errorf("`yolo host env %s`: rc=%d, want 2 and %q\n%s",
				strings.Join(tc.flags, " "), rc, "yolo host env: "+want, errw.String())
		}
		if out.Len() != 0 {
			t.Errorf("`yolo host env %s` printed a script on a refusal:\n%s", strings.Join(tc.flags, " "), out.String())
		}
	}

	// `--` is never a value: `yolo -p -- claude` is a -p with no value, at both notches. The jail
	// row is the front door's: the rewrite puts "run" first, so -p meets the separator and not an
	// injected "run" token it would read as a profile name.
	var rc int
	_, errs := captureBoth(t, func() { rc = runRun(RewriteArgv([]string{"-p", "--", "claude"})) })
	if rc != 2 || !strings.Contains(errs, "yolo run: -p needs a value") {
		t.Errorf("jail `yolo -p -- claude`: rc=%d\n%s", rc, errs)
	}
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"-p", "--", "claude"}, &out, &errw, false, nil); rc != 2 ||
		!strings.Contains(errw.String(), "yolo host: -p needs a value") {
		t.Errorf("host `yolo host -p -- claude`: rc=%d\n%s", rc, errw.String())
	}
	// And the grant: an empty glued value is the same refusal at the host.
	errw.Reset()
	if rc := hostMain([]string{withCredentialsFlag + "=", "--", "true"}, &out, &errw, false, nil); rc != 2 ||
		!strings.Contains(errw.String(), "yolo host: --with-credentials needs a value") {
		t.Errorf("host `yolo host --with-credentials= -- true`: rc=%d\n%s", rc, errw.String())
	}
}

// THE GLUED SHORT SPELLING WORKS AT BOTH NOTCHES. `-p=zai` is `-p zai`: in a jail (it always
// was), at `yolo host --` (which refused it with the whole usage) and at `yolo host env`. The
// host rows run the real launch and compare it with the separate spelling's, so either side
// regressing to a refusal fails.
func TestGluedShortProfileWorksAtBothNotches(t *testing.T) {
	var opts run.Options
	if parsed := parseRunArgs([]string{"run", "-p=zai", "--", "claude"}, &opts); parsed.misuse != nil ||
		opts.ProfileName != "zai" {
		t.Errorf("jail -p=zai: ProfileName=%q misuse=%v, want zai and none", opts.ProfileName, parsed.misuse)
	}

	valueFlagHome(t, `{"packs":["zai"]}`)
	t.Setenv("ZAI_API_KEY", "test-key")
	for _, spelling := range [][]string{{"-p", "zai"}, {"-p=zai"}, {"--profile=zai"}} {
		rc, reached, errw := hostExecRun(t, "bash", spelling...)
		if rc != 0 || !reached {
			t.Errorf("host `yolo host %s -- bash`: rc=%d reached=%v, want the launch\n%s",
				strings.Join(spelling, " "), rc, reached, errw)
		}
	}
	var sep, glued, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "bash", "-p", "zai"}, &sep, &errw, false, nil); rc != 0 {
		t.Fatalf("`yolo host env --agent bash -p zai`: rc=%d\n%s", rc, errw.String())
	}
	if rc := hostMain([]string{"env", "--agent", "bash", "-p=zai"}, &glued, &errw, false, nil); rc != 0 {
		t.Fatalf("`yolo host env --agent bash -p=zai`: rc=%d\n%s", rc, errw.String())
	}
	if sep.String() != glued.String() {
		t.Errorf("`-p=zai` and `-p zai` composed different scripts:\n%s\n---\n%s", sep.String(), glued.String())
	}
}

// ONE READER, ONE SKIP LIST. Every flag spelling any parser in this package passes to
// readValueFlag must be a value flag the subcommand scans skip (valueTakingFlags), or its value
// could be read as a subcommand name — `yolo --network host -- bash` read as the host verb was
// that defect. Read from the source, so a parser that learns a value flag without adding it to
// launchValueFlags fails here.
func TestEveryValueFlagAParserReadsIsSkipped(t *testing.T) {
	_, funcs := parseCLISource(t)
	seen := 0
	for _, fn := range funcs {
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "readValueFlag" {
				return true
			}
			for _, arg := range call.Args[2:] {
				var name string
				switch a := arg.(type) {
				case *ast.BasicLit:
					if a.Kind != token.STRING {
						continue
					}
					name, _ = strconv.Unquote(a.Value)
				case *ast.Ident:
					if a.Name != "withCredentialsFlag" {
						continue
					}
					name = withCredentialsFlag
				default:
					continue
				}
				seen++
				if !valueTakingFlags[name] {
					t.Errorf("%s reads value flag %q, which valueTakingFlags does not skip — "+
						"add it to launchValueFlags", fn.Name.Name, name)
				}
			}
			return true
		})
	}
	if seen == 0 {
		t.Fatal("found no readValueFlag call — the source scan is out of step with the parsers")
	}
}

// The reader itself: the next token whatever it looks like, except `--`; a glued value; and a
// missing or empty value named.
func TestReadValueFlag(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		value     string
		last      int
		wantError bool
	}{
		{[]string{"-p", "zai"}, "zai", 1, false},
		{[]string{"-p", "-h"}, "-h", 1, false},
		{[]string{"-p=zai"}, "zai", 0, false},
		{[]string{"--profile=claude=zai"}, "claude=zai", 0, false},
		{[]string{"-p"}, "", 0, true},
		{[]string{"-p", "--"}, "", 0, true},
		{[]string{"--profile="}, "", 0, true},
	} {
		f, ok := readValueFlag(tc.args, 0, "--profile", "-p")
		if !ok {
			t.Errorf("readValueFlag(%q) did not recognise the flag", tc.args)
			continue
		}
		if f.value != tc.value || f.last != tc.last || (f.err != nil) != tc.wantError {
			t.Errorf("readValueFlag(%q) = value %q last %d err %v, want %q %d error=%v",
				tc.args, f.value, f.last, f.err, tc.value, tc.last, tc.wantError)
		}
	}
	if _, ok := readValueFlag([]string{"--profiles"}, 0, "--profile", "-p"); ok {
		t.Error("readValueFlag read --profiles as --profile")
	}
}
