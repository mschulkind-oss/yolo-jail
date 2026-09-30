package run

// contracttags_test.go pins the attach's contract gate (contracttags.go;
// docs/design/attach-skew-and-contract-guardrails.md, OQ-SK1 to OQ-SK3): which tags a jail has,
// which an attach needs, and the disposition when one is missing — the acknowledgment, the
// terminal's restart prompt, and the refusal — driven through the real attachExisting and, for
// the restart's continuation into a fresh launch, through runContainer.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestJailContractTags pins how a running jail's tags are read: the list is authoritative when
// present (even empty), and a jail launched before the list gets each tag inferred from the
// marker its contract left — the credential gate's legacy YOLO_AGENT_ENV_FILES, and the absence
// of a frozen YOLO_PROVIDERS. An inspect that returned nothing proves nothing.
func TestJailContractTags(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		known bool
		want  []string
	}{
		{"this build's launch", currentJailEnv, true, launchContractTags},
		{"an explicit empty list is authoritative", "YOLO_VERSION=1\n" + entrypoint.ContractTagsEnv + "=\n", true, nil},
		{"an explicit list beats the legacy markers", "YOLO_AGENT_ENV_FILES=1\n" + entrypoint.ContractTagsEnv + "=entry-channel\n", true, []string{contractEntryChannel}},
		{"a gate-era jail keeps its per-agent files", gateEraJailEnv, true, []string{contractAgentEnvFiles, contractEntryChannel}},
		{"a pre-gate jail has the entry channel only", preGateEnv, true, []string{contractEntryChannel}},
		{"a pre-change jail has neither", preChangeEnv, true, nil},
		{"an empty inspect proves nothing", "", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tags, known := jailContractTags(strings.Split(tc.env, "\n"))
			if known != tc.known {
				t.Fatalf("known = %v, want %v", known, tc.known)
			}
			var got []string
			for tag := range tags {
				got = append(got, tag)
			}
			sort.Strings(got)
			want := slices.Clone(tc.want)
			sort.Strings(want)
			if !slices.Equal(got, want) {
				t.Errorf("tags = %v, want %v", got, want)
			}
		})
	}
}

// TestAttachContractNeeds pins what an attach asks for: nothing when it delivers no selection
// and scopes nothing; the entry channel for a selection and the per-agent files for a scoped
// value, naming what each withholds (names, never values); and nothing at all for the plain
// re-entry into a pre-change jail, whose launch-time delivery stands in.
func TestAttachContractNeeds(t *testing.T) {
	packs := zaiSelected(t)
	t.Run("no selection", func(t *testing.T) {
		_, _, channel, _ := attachFixture(t, currentJailEnv, packs, hydratedKey(), nil)
		if c := attachContractFor(channel, strings.Split(currentJailEnv, "\n")); len(c.needs) != 0 || c.standIn {
			t.Errorf("an attach delivering no selection needs nothing: %+v", c)
		}
	})
	t.Run("a scoped selection", func(t *testing.T) {
		_, _, channel, _ := attachFixture(t, currentJailEnv, packs, hydratedKey(),
			func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })
		c := attachContractFor(channel, strings.Split(currentJailEnv, "\n"))
		var tags []string
		joined := ""
		for _, n := range c.needs {
			tags = append(tags, n.tag)
			joined += strings.Join(n.withheld, "\n") + "\n"
		}
		if !slices.Equal(tags, []string{contractEntryChannel, contractAgentEnvFiles}) {
			t.Errorf("needs = %v, want the entry channel and the per-agent files", tags)
		}
		for _, want := range []string{"claude=zai", "claude (profile zai): ", "ZAI_API_KEY"} {
			if !strings.Contains(joined, want) {
				t.Errorf("the withheld lines must name %q:\n%s", want, joined)
			}
		}
		if strings.Contains(joined, "tok-9") {
			t.Errorf("the withheld lines carry a credential VALUE:\n%s", joined)
		}
	})
	t.Run("a pre-change jail's own selection", func(t *testing.T) {
		_, _, channel, _ := attachFixture(t, preChangeEnv, preChangePacks(t), hydratedKey(),
			func(_ *Options, cfg *jsonx.OrderedMap) { configSelects(cfg, "claude", "zai") })
		if c := attachContractFor(channel, strings.Split(preChangeEnv, "\n")); !c.standIn || len(c.needs) != 0 {
			t.Errorf("the plain re-entry must stand in with no needs: %+v", c)
		}
	})
}

// TestEveryTagAnAttachCanNeedIsFrozenByTheLaunch: a need whose tag this build's launch does not
// freeze would make this build refuse every jail it launched itself. Read off attachContractFor's
// source rather than a fixture channel, so a need added tomorrow is covered without a new fixture.
func TestEveryTagAnAttachCanNeedIsFrozenByTheLaunch(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "contracttags.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "attachContractFor" {
			fn = fd
		}
	}
	if fn == nil {
		t.Fatal("contracttags.go has no attachContractFor")
	}
	values := map[string]string{contractEntryChannel: contractEntryChannel, contractAgentEnvFiles: contractAgentEnvFiles,
		contractProfileSets: contractProfileSets}
	byName := map[string]string{"contractEntryChannel": contractEntryChannel, "contractAgentEnvFiles": contractAgentEnvFiles,
		"contractProfileSets": contractProfileSets}
	var needed []string
	ast.Inspect(fn, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "tag" {
			id, ok := kv.Value.(*ast.Ident)
			if !ok {
				t.Errorf("a need's tag must be one of the named constants, not %T", kv.Value)
				return true
			}
			v, known := byName[id.Name]
			if !known {
				t.Errorf("attachContractFor needs %s, which this test does not know — add it to byName "+
					"and to launchContractTags", id.Name)
				return true
			}
			needed = append(needed, v)
		}
		return true
	})
	if len(needed) == 0 {
		t.Fatal("found no needs in attachContractFor, so this test checks nothing")
	}
	for _, tag := range needed {
		if !slices.Contains(launchContractTags, values[tag]) {
			t.Errorf("an attach can need %q, but no launch freezes it: this build would refuse "+
				"the jails it launches itself", tag)
		}
	}
}

// skewRuntime fakes the runtime an attach and a restart talk to: a jail running with env as its
// frozen environment and execIDs live exec sessions, which a `stop` ends.
type skewRuntime struct {
	env     string
	execIDs string
	stops   []string
	// stuck keeps the container running through a stop.
	stuck   bool
	stopped bool
}

func (r *skewRuntime) exec(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
	joined := strings.Join(argv, " ")
	switch {
	case len(argv) > 1 && argv[1] == "inspect" && strings.Contains(joined, "ExecIDs"):
		return ExecResult{Ran: true, RC: 0, Stdout: r.execIDs + "\n"}
	case len(argv) > 1 && argv[1] == "inspect":
		return ExecResult{Ran: true, RC: 0, Stdout: r.env}
	case len(argv) > 1 && argv[1] == "stop":
		r.stops = append(r.stops, argv[len(argv)-1])
		r.stopped = !r.stuck
		return ExecResult{Ran: true, RC: 0}
	case len(argv) > 1 && argv[1] == "ps" && strings.Contains(joined, "--format"):
		return ExecResult{Ran: true, RC: 0} // liveYoloContainers: none owned here
	case len(argv) > 1 && argv[1] == "ps":
		if r.stopped {
			return ExecResult{Ran: true, RC: 0}
		}
		return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
	case len(argv) > 1 && argv[1] == "rm":
		return ExecResult{Ran: true, RC: 1}
	}
	return ExecResult{Ran: false}
}

// skewAttach is one attach against a pre-gate jail with a typed zai selection — the jail lacks
// agent-env-files, which that selection needs — driven through attachExisting with a fake
// runtime on PATH for the exec. tty sets both terminal predicates; stdin is what a prompt reads.
type skewAttach struct {
	o        *Options
	rt       *skewRuntime
	cfg      *jsonx.OrderedMap
	packs    []*packload.Pack
	channel  *packChannel
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	stdin    *strings.Reader
	envFile  string
	before   []byte
	released bool
	execed   string
}

func newSkewAttach(t *testing.T, ttyIn, ttyOut bool, stdin string, env map[string]string) *skewAttach {
	t.Helper()
	packs := zaiSelected(t)
	o, cfg, channel, stderr := attachFixture(t, preGateEnv, packs, hydratedKey(),
		func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })
	s := &skewAttach{o: o, rt: &skewRuntime{env: preGateEnv, execIDs: "2"}, cfg: cfg, packs: packs,
		channel: channel, stderr: stderr, stdout: &bytes.Buffer{}, stdin: strings.NewReader(stdin)}
	o.Stdout = s.stdout
	o.Stdin = s.stdin
	o.Exec = s.rt.exec
	o.IsTTYStdin = func() bool { return ttyIn }
	o.IsTTYStdout = func() bool { return ttyOut }
	o.Getenv = func(k string) string { return env[k] }
	s.envFile, s.before = seedLiveChannelFile(t, o)
	s.execed = filepath.Join(t.TempDir(), "execed")
	bin := t.TempDir()
	script := "#!/bin/sh\n: > '" + s.execed + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return s
}

func (s *skewAttach) attach() (int, bool) {
	return s.o.attachExisting("yolo-ws-abcd1234", "podman", "true", s.cfg,
		stagedPacks{root: "/ctx/packs", packs: s.packs}, s.channel, false, func() { s.released = true })
}

func (s *skewAttach) didExec() bool {
	_, err := os.Stat(s.execed)
	return err == nil
}

// TestAttachSkewPromptsARestartInATerminal: with a terminal on both ends, a missing contract
// asks `Restart jail now? [Y/n]`, naming what differs and the sessions the stop ends. Yes — or
// Enter, the capital letter — stops the jail and reports restarted without an exec, a write, or
// a release of the lock the fresh launch continues under.
func TestAttachSkewPromptsARestartInATerminal(t *testing.T) {
	for _, answer := range []string{"y\n", "\n", "YES\n"} {
		t.Run(strings.TrimSpace(answer)+"<enter>", func(t *testing.T) {
			s := newSkewAttach(t, true, true, answer, nil)
			rc, restarted := s.attach()
			if !restarted || rc != 0 {
				t.Fatalf("a yes must restart: rc=%d restarted=%v\nstdout:\n%s\nstderr:\n%s",
					rc, restarted, s.stdout, s.stderr)
			}
			if !slices.Equal(s.rt.stops, []string{"yolo-ws-abcd1234"}) {
				t.Errorf("the restart must stop this jail, once: %v", s.rt.stops)
			}
			out := s.stdout.String()
			for _, want := range []string{"Restart jail now? [Y/n]", "agent-env-files",
				"claude (profile zai): ", "ZAI_API_KEY", "every session in it (3 running now)"} {
				if !strings.Contains(out, want) {
					t.Errorf("the prompt must name %q:\n%s", want, out)
				}
			}
			if s.didExec() {
				t.Error("a restarted attach exec'd into the jail it just stopped")
			}
			if s.released {
				t.Error("a restart released the launch lock the fresh launch continues under")
			}
			assertLiveChannelFileUnchanged(t, s.envFile, s.before)
		})
	}
}

// TestAttachSkewDeclinedRefuses: no, and end of input, refuse — declining never proceeds on its
// own (OQ-SK1), and a closed stdin is nobody answering, not consent to the default.
func TestAttachSkewDeclinedRefuses(t *testing.T) {
	for _, tc := range []struct{ name, answer string }{{"no", "n\n"}, {"end of input", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSkewAttach(t, true, true, tc.answer, nil)
			rc, restarted := s.attach()
			if rc != 1 || restarted {
				t.Fatalf("a declined restart must refuse: rc=%d restarted=%v\n%s", rc, restarted, s.stderr)
			}
			if len(s.rt.stops) != 0 || s.didExec() {
				t.Errorf("a declined restart stopped (%v) or exec'd (%v)", s.rt.stops, s.didExec())
			}
			if !s.released {
				t.Error("a refusal must release the caller's launch lock")
			}
			for _, want := range []string{"Refusing to attach", "'yolo stop'", AllowAttachSkewEnv} {
				if !strings.Contains(s.stderr.String(), want) {
					t.Errorf("the refusal must name %q:\n%s", want, s.stderr)
				}
			}
			assertLiveChannelFileUnchanged(t, s.envFile, s.before)
		})
	}
}

// TestAttachSkewWithoutATerminalRefuses: no terminal on either end means no prompt — and no
// read of stdin, whose "y" a script piped in is not a person — only the refusal, naming the
// restart with the sessions it ends and the acknowledgment with what it costs.
func TestAttachSkewWithoutATerminalRefuses(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ttyIn, ttyOut bool
	}{{"neither", false, false}, {"stdin only", true, false}, {"stdout only", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSkewAttach(t, tc.ttyIn, tc.ttyOut, "y\n", nil)
			rc, restarted := s.attach()
			if rc != 1 || restarted {
				t.Fatalf("rc=%d restarted=%v\n%s", rc, restarted, s.stderr)
			}
			if len(s.rt.stops) != 0 || s.didExec() {
				t.Errorf("the refusal stopped (%v) or exec'd (%v)", s.rt.stops, s.didExec())
			}
			if s.stdin.Len() != len("y\n") {
				t.Error("the refusal read stdin, so a piped answer could restart a jail")
			}
			out := s.stderr.String()
			for _, want := range []string{"Refusing to attach", "agent-env-files", "ZAI_API_KEY",
				"'yolo stop'", "every session in it (3 running now)", AllowAttachSkewEnv + "=1"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal must name %q:\n%s", want, out)
				}
			}
			if strings.Contains(s.stdout.String(), "Restart jail now") {
				t.Errorf("no terminal, no prompt:\n%s", s.stdout)
			}
			assertLiveChannelFileUnchanged(t, s.envFile, s.before)
		})
	}
}

// TestAttachSkewAcknowledgedProceedsWithoutDelivery: YOLO_ALLOW_ATTACH_SKEW is the one
// acknowledgment. It proceeds — without asking, even at a terminal — says loudly what differs
// and what is withheld, and degrades on the host: nothing of this entry's channel is written.
func TestAttachSkewAcknowledgedProceedsWithoutDelivery(t *testing.T) {
	s := newSkewAttach(t, true, true, "n\n", map[string]string{AllowAttachSkewEnv: "1"})
	rc, restarted := s.attach()
	if rc != 0 || restarted || !s.didExec() {
		t.Fatalf("the acknowledgment must proceed to the exec: rc=%d restarted=%v execed=%v\n%s",
			rc, restarted, s.didExec(), s.stderr)
	}
	if len(s.rt.stops) != 0 || strings.Contains(s.stdout.String(), "Restart jail now") {
		t.Errorf("the acknowledgment must neither stop nor ask:\n%s", s.stdout)
	}
	out := s.stderr.String()
	for _, want := range []string{AllowAttachSkewEnv, "agent-env-files", "claude (profile zai): ",
		"ZAI_API_KEY", "delivers nothing"} {
		if !strings.Contains(out, want) {
			t.Errorf("the disclosure must name %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "claude only") {
		t.Errorf("no per-agent disclosure for a delivery that did not happen:\n%s", out)
	}
	assertLiveChannelFileUnchanged(t, s.envFile, s.before)
}

// yoloEnvSpellings is every YOLO_* variable the tree's non-test Go source spells, read fresh so an
// override added tomorrow is in the set without an edit here. Every one, not only the YOLO_ALLOW_*
// hatches: an override need not be spelled like one (YOLO_NO_BANNER,
// YOLO_BYPASS_SHIMS, YOLO_NO_HOST_LOOPBACK).
func yoloEnvSpellings(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`YOLO_[A-Z0-9_]+`)
	seen := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range re.FindAllString(string(b), -1) {
				seen[strings.TrimRight(m, "_")] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// overrideOptionBool matches an Options field that overrides a check the way a hatch does.
var overrideOptionBool = regexp.MustCompile(`^(Accept|Allow|Assume|Bypass|Force|Ignore|Skip|Yes)`)

// TestNoOtherHatchAcknowledgesAttachSkew: the ruling's words are "it shouldn't just silently
// ride along, even if there's another similar override flag". Every other YOLO_* variable the tree
// spells, set at once, and every override-style Options field set true, still refuse.
func TestNoOtherHatchAcknowledgesAttachSkew(t *testing.T) {
	spellings := yoloEnvSpellings(t)
	for _, must := range []string{AllowAttachSkewEnv, "YOLO_ALLOW_SOURCE_SKEW", "YOLO_ALLOW_MISSING_PROVIDERS",
		"YOLO_ALLOW_STALE_IMAGE", "YOLO_ALLOW_UNREACHABLE_SERVICES",
		"YOLO_BYPASS_SHIMS", "YOLO_NO_HOST_LOOPBACK"} {
		if !slices.Contains(spellings, must) {
			t.Fatalf("the census found no %s, so it is not reading the tree: %v", must, spellings)
		}
	}
	env := map[string]string{}
	for _, h := range spellings {
		if h != AllowAttachSkewEnv {
			env[h] = "1"
		}
	}
	s := newSkewAttach(t, false, false, "", env)
	var set []string
	v := reflect.ValueOf(s.o).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if f.IsExported() && f.Type.Kind() == reflect.Bool && overrideOptionBool.MatchString(f.Name) {
			v.Field(i).SetBool(true)
			set = append(set, f.Name)
		}
	}
	if !slices.Contains(set, "AcceptConfigChanges") {
		t.Fatalf("the Options census found no AcceptConfigChanges, so it is not reading the struct: %v", set)
	}
	rc, restarted := s.attach()
	if rc != 1 || restarted || s.didExec() {
		t.Fatalf("another override acknowledged the attach skew (%d variables, Options %v): rc=%d "+
			"restarted=%v execed=%v\n%s", len(env), set, rc, restarted, s.didExec(), s.stderr)
	}
	if !strings.Contains(s.stderr.String(), "Refusing to attach") {
		t.Errorf("the attach must refuse:\n%s", s.stderr)
	}
	assertLiveChannelFileUnchanged(t, s.envFile, s.before)
}

// TestARestartThatCannotStopTheJailRefuses: a jail still running after the stop cannot be
// launched beside, so the restart refuses rather than report a restart the fresh launch would
// then collide with.
func TestARestartThatCannotStopTheJailRefuses(t *testing.T) {
	attempts, interval := restartPollAttempts, restartPollInterval
	restartPollAttempts, restartPollInterval = 2, time.Millisecond
	t.Cleanup(func() { restartPollAttempts, restartPollInterval = attempts, interval })
	s := newSkewAttach(t, true, true, "y\n", nil)
	s.rt.stuck = true
	rc, restarted := s.attach()
	if rc != 1 || restarted || s.didExec() {
		t.Fatalf("rc=%d restarted=%v execed=%v\n%s", rc, restarted, s.didExec(), s.stderr)
	}
	if !strings.Contains(s.stderr.String(), "did not stop") {
		t.Errorf("the refusal must say the jail did not stop:\n%s", s.stderr)
	}
}

// TestARestartedAttachContinuesAsAFreshLaunch is the behavioral pin on runContainer's first
// attach site: a restart must fall through into the fresh launch rather than return. The fresh
// launch here stops at the jail prefix (no prebuilt binaries, and a build seam that fails), which
// is far enough to prove it began.
func TestARestartedAttachContinuesAsAFreshLaunch(t *testing.T) {
	s := newSkewAttach(t, true, true, "y\n", nil)
	s.o.AcceptConfigChanges = true
	built := false
	s.o.BuildJailPrefix = func(string) (string, []string) { built = true; return "", nil }
	rc := s.o.runContainer(s.cfg, "podman", t.TempDir(), "yolo-ws-abcd1234",
		stagedPacks{root: "/ctx/packs", packs: s.packs}, nil, s.channel, nil)
	if rc != 1 {
		t.Fatalf("the fixture's fresh launch must stop at the prefix build: rc=%d\n%s", rc, s.stderr)
	}
	if !slices.Equal(s.rt.stops, []string{"yolo-ws-abcd1234"}) {
		t.Fatalf("the attach never restarted the jail: stops=%v\nstdout:\n%s", s.rt.stops, s.stdout)
	}
	if !built {
		t.Fatalf("the restart returned instead of continuing into the fresh launch\nstdout:\n%s\nstderr:\n%s",
			s.stdout, s.stderr)
	}
	if strings.Contains(s.stdout.String(), "Attaching to existing jail") || s.didExec() {
		t.Errorf("a restarted attach went on to attach:\n%s", s.stdout)
	}
}

// TestEveryAttachSiteKeepsTheRestart pins runContainer's other attach sites, which a unit
// fixture cannot reach (each needs a jail that appears while this launch waits): every one must be
// exactly `if rc, restarted := o.attachExisting(...); !restarted { return rc }`. A site that
// discards restarted returns 0 with no jail after a restart; one that ignores or inverts it either
// returns after a restart or falls through into a fresh launch beside a running jail. The first
// site's behavior is also driven (TestARestartedAttachContinuesAsAFreshLaunch); this pins the
// shape every site shares, so the three cannot drift apart.
func TestEveryAttachSiteKeepsTheRestart(t *testing.T) {
	fn := methodDecl(t, "run.go", "runContainer")
	isAttach := func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "attachExisting"
	}
	calls, sites := 0, 0
	ast.Inspect(fn, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.CallExpr:
			if isAttach(st) {
				calls++
			}
		case *ast.IfStmt:
			init, ok := st.Init.(*ast.AssignStmt)
			if !ok || len(init.Rhs) != 1 || !isAttach(init.Rhs[0]) {
				return true
			}
			sites++
			if len(init.Lhs) != 2 {
				t.Errorf("an attach site assigns %d results; it must keep both", len(init.Lhs))
				return true
			}
			rc, _ := init.Lhs[0].(*ast.Ident)
			restarted, _ := init.Lhs[1].(*ast.Ident)
			if rc == nil || restarted == nil || rc.Name == "_" || restarted.Name == "_" {
				t.Error("an attach site discards one of attachExisting's results")
				return true
			}
			cond, _ := st.Cond.(*ast.UnaryExpr)
			var negated *ast.Ident
			if cond != nil {
				negated, _ = cond.X.(*ast.Ident)
			}
			if cond == nil || cond.Op != token.NOT || negated == nil || negated.Name != restarted.Name {
				t.Errorf("an attach site's condition is not exactly !%s", restarted.Name)
			}
			if st.Else != nil || len(st.Body.List) != 1 {
				t.Error("an attach site's body is not exactly one return, with no else")
				return true
			}
			ret, ok := st.Body.List[0].(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				t.Error("an attach site's body is not `return rc`")
				return true
			}
			if id, ok := ret.Results[0].(*ast.Ident); !ok || id.Name != rc.Name {
				t.Errorf("an attach site does not return attachExisting's own rc (%s)", rc.Name)
			}
		}
		return true
	})
	if sites != calls {
		t.Errorf("runContainer calls attachExisting %d times, and %d of them are the pinned "+
			"`if rc, restarted := o.attachExisting(...); !restarted { return rc }`", calls, sites)
	}
	if sites < 3 {
		t.Errorf("found %d attach sites in runContainer, want the three (the first look, the raced "+
			"re-check, the stale removal's wait)", sites)
	}
}
