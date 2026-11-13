package run

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const checkedFixtureIntegrity = "sha512-RF0Fw+rO5AMf9MAyaRXI4AV0Ulj5lMHqVxxdSgiVbixSCXoEmmX/jk0CuJw4+3SqroYO9VoUh+HcuJivvtJemA=="

// checkedPrivateClientFixture is a producer's checked delivery of one selected client, unbound to
// a generation, as prepareOrdinaryPrograms hands it to the keeper.
func checkedPrivateClientFixture(bin, pack, front, recipePack, recipeIdentity string) callbrokerinput.PreparedHTTPClientEntrypoint {
	root := ".local/lib/yolo-npm/" + strings.Repeat("e", 64)
	return callbrokerinput.PreparedHTTPClientEntrypoint{Bin: bin, Pack: pack, Front: front, RecipePack: recipePack,
		RecipeIdentity: recipeIdentity, Declared: "semver@7.7.2", Platform: "linux/amd64", Runtime: `{"Image":"i","NodeDigest":"3333333333333333333333333333333333333333333333333333333333333333"}`,
		Compatibility: strings.Repeat("e", 64), Release: "7.7.2", SourceURL: "https://registry.npmjs.org/semver/-/semver-7.7.2.tgz",
		SourceIntegrity: checkedFixtureIntegrity, Bootstrap: entrypoint.PrivateClientBootstrap,
		Binding: entrypoint.PrivateClientBinding{Bin: bin, CaptureKey: strings.Repeat("f", 16), RuntimeRoot: root,
			Entrypoint: root + "/node_modules/semver/bin/semver.js", EntrypointSHA256: "sha256:" + strings.Repeat("1", 64),
			RuntimeFiles: "sha256:" + strings.Repeat("2", 64), NodeSHA256: "sha256:" + strings.Repeat("3", 64)}}
}

func checkedPrivateClientsForCells(cells []packload.PrivateProgramEnvPlan) []callbrokerinput.PreparedHTTPClientEntrypoint {
	out := make([]callbrokerinput.PreparedHTTPClientEntrypoint, 0, len(cells))
	for _, c := range cells {
		out = append(out, checkedPrivateClientFixture(c.Bin, c.Pack, c.Front, c.RecipePack, c.RecipeIdentity))
	}
	return out
}

// writePreparedEntrypointsTestFile writes the entrypoints record the producer publishes beside a
// prepared generation's file.
func writePreparedEntrypointsTestFile(t *testing.T, preparedPath string, g *callbrokerinput.PreparedHTTPClientGeneration) {
	t.Helper()
	r := callbrokerinput.PreparedHTTPClientEntrypoints{Version: callbrokerinput.PreparedHTTPClientEntrypointsVersion,
		Generation: g.Generation, Container: g.Container, Loophole: g.Loophole}
	for _, c := range g.Clients {
		checked := checkedPrivateClientFixture(c.Bin, c.Pack, c.Front, c.RecipePack, c.RecipeIdentity)
		checked.Binding.Generation = g.Generation
		r.Clients = append(r.Clients, checked)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(preparedPath), preparedHTTPEntrypointsLeaf(filepath.Base(preparedPath))), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPrivateClientDeliveriesRefusesMissingAndMismatchedDeliveries(t *testing.T) {
	cell := packload.PrivateProgramEnvPlan{Bin: "semver", Pack: "audited-semver", Front: "tasks", RecipePack: "audited-semver",
		RecipeIdentity: "sha256:" + strings.Repeat("a", 64)}
	good := checkedPrivateClientFixture(cell.Bin, cell.Pack, cell.Front, cell.RecipePack, cell.RecipeIdentity)
	bound, refusal := checkPrivateClientDeliveries([]packload.PrivateProgramEnvPlan{cell}, map[string]callbrokerinput.PreparedHTTPClientEntrypoint{"semver": good})
	if refusal != nil || len(bound) != 1 || bound[0] != good {
		t.Fatalf("a checked delivery was not bound: %v %+v", refusal, bound)
	}
	for name, tc := range map[string]struct {
		verified map[string]callbrokerinput.PreparedHTTPClientEntrypoint
		cause    string
		repair   string
	}{
		"missing delivery": {nil, "delivered no checked capture", "supported baseline runtime"},
		"same-key recipe": {map[string]callbrokerinput.PreparedHTTPClientEntrypoint{"semver": func() callbrokerinput.PreparedHTTPClientEntrypoint {
			c := good
			c.RecipeIdentity = "sha256:" + strings.Repeat("b", 64)
			return c
		}()}, "another selected recipe", "selected recipe is prepared again"},
		"source fork": {map[string]callbrokerinput.PreparedHTTPClientEntrypoint{"semver": func() callbrokerinput.PreparedHTTPClientEntrypoint {
			c := good
			c.SourceURL = "git+https://example.invalid/semver"
			return c
		}()}, "audited source URL", "supported baseline runtime"},
		"other bootstrap": {map[string]callbrokerinput.PreparedHTTPClientEntrypoint{"semver": func() callbrokerinput.PreparedHTTPClientEntrypoint {
			c := good
			c.Bootstrap = "yolo"
			return c
		}()}, "shipped bootstrap", "supported baseline runtime"},
	} {
		t.Run(name, func(t *testing.T) {
			_, refusal := checkPrivateClientDeliveries([]packload.PrivateProgramEnvPlan{cell}, tc.verified)
			if refusal == nil {
				t.Fatal("an unchecked delivery was bound")
			}
			out := refusal.Render(false)
			for _, want := range []string{"semver", "audited-semver", "tasks", tc.cause, tc.repair, "Nothing falls back"} {
				if !strings.Contains(out, want) {
					t.Errorf("refusal does not name %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestBindPrivateClientCommandStartsTheBoundBootstrapOnly(t *testing.T) {
	b := entrypoint.PrivateClientBinding{Bin: "semver", Generation: strings.Repeat("a", 32), CaptureKey: strings.Repeat("f", 16),
		RuntimeRoot: ".local/lib/yolo-npm/x", Entrypoint: ".local/lib/yolo-npm/x/node_modules/semver/bin/semver.js",
		EntrypointSHA256: "sha256:" + strings.Repeat("1", 64), RuntimeFiles: "sha256:" + strings.Repeat("2", 64), NodeSHA256: "sha256:" + strings.Repeat("3", 64)}
	got, err := bindPrivateClientCommand("semver 1.2.3 -r '>1'", []entrypoint.PrivateClientBinding{b}, nil)
	argv := entrypoint.PrivateClientArgv(b)
	if err != nil || !strings.HasPrefix(got, entrypoint.PrivateClientBootstrap+" ") || !strings.HasSuffix(got, " 1.2.3 -r '>1'") {
		t.Fatalf("bound command = %q, %v", got, err)
	}
	if argv[0] != entrypoint.PrivateClientBootstrap {
		t.Fatalf("argv does not start the shipped bootstrap: %q", argv)
	}
	for _, other := range []string{"semverx 1", "bash -c semver", "claude"} {
		if got, err := bindPrivateClientCommand(other, []entrypoint.PrivateClientBinding{b}, []string{"semver"}); err != nil || got != other {
			t.Fatalf("an ordinary command %q was rewritten: %q %v", other, got, err)
		}
	}
	if _, err := bindPrivateClientCommand("semver 1", nil, []string{"semver"}); err == nil {
		t.Fatal("a private client with no binding fell back to its ordinary launcher")
	}
}

// attachRecordingArgs attaches to the fixture's running jail with targetCmd and returns the
// argv the runtime was exec'd with (nil if it was not).
func attachRecordingArgs(t *testing.T, f attachedPrivateHTTP, targetCmd string) (int, []string) {
	t.Helper()
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + shquote.Quote(record) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	rc, _ := f.o.attachExisting("yolo-ws-abcd1234", "podman", targetCmd, f.cfg, stagedPacks{root: "/ctx/packs", packs: f.packs}, f.channel, false, nil)
	data, err := os.ReadFile(record)
	if err != nil {
		return rc, nil
	}
	return rc, strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

func TestAttachStartsAPrivateClientThroughItsPublishedBinding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, attachedPrivateHTTP)
		want   bool
	}{
		{"published binding", nil, true},
		{"missing entrypoints record", func(t *testing.T, f attachedPrivateHTTP) {
			if err := os.Remove(f.preparedPath + ".entrypoints"); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"entrypoints of another generation", func(t *testing.T, f attachedPrivateHTTP) {
			other := f.prepared
			other.Generation = strings.Repeat("f", 32)
			writePreparedEntrypointsTestFile(t, f.preparedPath, &other)
		}, false},
		{"entrypoints of another recipe", func(t *testing.T, f attachedPrivateHTTP) {
			other := f.prepared
			other.Clients = append([]callbrokerinput.PreparedHTTPClientPlan(nil), other.Clients...)
			other.Clients[0].RecipeIdentity = "sha256:" + strings.Repeat("b", 64)
			writePreparedEntrypointsTestFile(t, f.preparedPath, &other)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := attachedPrivateHTTPFixture(t, true, true)
			if tc.mutate != nil {
				tc.mutate(t, f)
			}
			prepared := 0
			f.o.PreparePrograms = func(ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
				prepared++
				return nil, nil
			}
			rc, argv := attachRecordingArgs(t, f, "fixture-client --version")
			if prepared != 0 {
				t.Fatalf("attach prepared programs %d times", prepared)
			}
			if !tc.want {
				if rc != 1 || argv != nil {
					t.Fatalf("attach without a valid binding ran: rc=%d argv=%q\n%s", rc, argv, f.stderr.String())
				}
				return
			}
			joined := strings.Join(argv, "\x00")
			want := strings.Join(entrypoint.PrivateClientArgv(func() entrypoint.PrivateClientBinding {
				c := checkedPrivateClientFixture("fixture-client", f.prepared.Clients[0].Pack, f.prepared.Clients[0].Front,
					f.prepared.Clients[0].RecipePack, f.prepared.Clients[0].RecipeIdentity)
				c.Binding.Generation = f.prepared.Generation
				return c.Binding
			}()), " ")
			if rc != 0 || !strings.Contains(strings.ReplaceAll(joined, "\x00", " "), want+" --version") {
				t.Fatalf("attach did not start the bound bootstrap: rc=%d argv=%q want %q\n%s", rc, argv, want, f.stderr.String())
			}
		})
	}
}

func TestFreshLaunchBindsItsFirstCommandThroughThePublishedRecord(t *testing.T) {
	f := attachedPrivateHTTPFixture(t, true, true)
	c := f.prepared.Clients[0]
	f.o.privateClientEntrypoints = []callbrokerinput.PreparedHTTPClientEntrypoint{checkedPrivateClientFixture(c.Bin, c.Pack, c.Front, c.RecipePack, c.RecipeIdentity)}
	staged := stagedPacks{root: f.tree, packs: f.packs}
	got := f.o.freshPrivateClientCommand("yolo-ws-abcd1234", "podman", staged, "fixture-client --version")
	if !strings.HasPrefix(got, entrypoint.PrivateClientBootstrap+" internal private-client-exec ") || !strings.Contains(got, "--generation="+f.prepared.Generation) {
		t.Fatalf("fresh command not bound: %q", got)
	}
	if got := f.o.freshPrivateClientCommand("yolo-ws-abcd1234", "podman", staged, "claude"); got != "claude" {
		t.Fatalf("an ordinary fresh command was rewritten: %q", got)
	}
	if err := os.Remove(f.preparedPath + ".entrypoints"); err != nil {
		t.Fatal(err)
	}
	got = f.o.freshPrivateClientCommand("yolo-ws-abcd1234", "podman", staged, "fixture-client --version")
	if strings.Contains(got, entrypoint.PrivateClientBootstrap) || !strings.Contains(got, "refusing to start") || !strings.HasSuffix(got, "exit 1") {
		t.Fatalf("fresh command without a published binding was not refused: %q", got)
	}
	if got := f.o.freshPrivateClientCommand("yolo-ws-abcd1234", "podman", staged, "claude"); got != "claude" {
		t.Fatalf("an ordinary fresh command was refused: %q", got)
	}
}

func TestPrepareOrdinaryProgramsBindsSelectedPrivateClientsOrRefuses(t *testing.T) {
	oldHost := hostPlatform
	t.Cleanup(func() { hostPlatform = oldHost })
	repoRoot := t.TempDir()
	for _, name := range []string{"flake.nix", "flake.lock"} {
		if err := os.WriteFile(filepath.Join(repoRoot, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name     string
		rt       string
		host     string
		approved bool
		deliver  func(ProgramCaptureRequest)
		calls    int
		want     string // "" is a bound launch
	}{
		{name: "checked delivery", rt: "podman", host: "linux/amd64", approved: true, calls: 1, deliver: func(req ProgramCaptureRequest) {
			c := req.PrivateClients[0]
			req.PrivateClientEntrypoints[c.Bin] = checkedPrivateClientFixture(c.Bin, c.Pack, c.Front, c.RecipePack, c.RecipeIdentity)
		}},
		{name: "missing delivery", rt: "podman", host: "linux/amd64", approved: true, calls: 1, want: "delivered no checked capture"},
		{name: "same-key recipe", rt: "podman", host: "linux/amd64", approved: true, calls: 1, deliver: func(req ProgramCaptureRequest) {
			c := req.PrivateClients[0]
			req.PrivateClientEntrypoints[c.Bin] = checkedPrivateClientFixture(c.Bin, c.Pack, c.Front, c.RecipePack, "sha256:"+strings.Repeat("b", 64))
		}, want: "another selected recipe"},
		{name: "another runtime", rt: "container", host: "linux/amd64", approved: true, want: "local Podman jail"},
		{name: "another host", rt: "podman", host: "darwin/arm64", approved: true, want: "linux/amd64 host"},
		{name: "unselected front", rt: "podman", host: "linux/amd64", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if containerJailPlatform() != privateClientPlatform && tc.rt == "podman" {
				t.Skip("the private client cell is linux/amd64 only")
			}
			hostPlatform = tc.host
			f := attachedPrivateHTTPFixture(t, true, false)
			o := f.o
			var stderr strings.Builder
			o.Stderr = &stderr
			o.approvedHTTPBindings = nil
			if tc.approved {
				o.approvedHTTPBindings = map[string]config.ApprovedCallBrokerHTTP{"tasks": {Pack: "front-pack"}}
			}
			calls := 0
			o.PreparePrograms = func(req ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
				calls++
				if tc.approved != (len(req.PrivateClients) == 1) {
					t.Fatalf("producer saw private clients %+v", req.PrivateClients)
				}
				if tc.deliver != nil {
					tc.deliver(req)
				}
				return map[string]entrypoint.ProgramCaptureDelivery{}, nil
			}
			rc := o.prepareOrdinaryPrograms(jsonx.NewOrderedMap(), tc.rt, repoRoot, f.packs, nil)
			if calls != tc.calls {
				t.Fatalf("producer called %d times, want %d", calls, tc.calls)
			}
			if tc.want == "" {
				if rc != 0 {
					t.Fatalf("rc=%d\n%s", rc, stderr.String())
				}
				if tc.approved && (len(o.privateClientEntrypoints) != 1 || o.privateClientEntrypoints[0].Bin != "fixture-client") {
					t.Fatalf("checked delivery not kept for the keeper: %+v", o.privateClientEntrypoints)
				}
				// Slice 3.4: each bound client's front joins the launch's jail daemons, once.
				fronts := privateHTTPFrontDaemons(o.privateClientEntrypoints)
				if !tc.approved {
					if len(fronts) != 0 {
						t.Fatalf("an unselected front started: %+v", fronts)
					}
					return
				}
				want := loopholes.JailDaemonSpec{Name: "http-front-tasks",
					Cmd:     []string{"yolo-jaild", "call-broker-http-front", "--endpoint", "/run/yolo-services/tasks.endpoint", "--loophole", "tasks"},
					Restart: "no", RequiredReady: true}
				if len(fronts) != 1 || !reflect.DeepEqual(fronts[0], want) {
					t.Fatalf("front daemons = %+v, want exactly %+v", fronts, want)
				}
				return
			}
			out := stderr.String()
			if rc != 1 || len(o.privateClientEntrypoints) != 0 {
				t.Fatalf("an unbound private client launched: rc=%d %+v\n%s", rc, o.privateClientEntrypoints, out)
			}
			for _, want := range []string{"fixture-client", "client-pack", "tasks", tc.want, "Repair:", "Nothing falls back"} {
				if !strings.Contains(out, want) {
					t.Errorf("refusal does not name %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestTheFreshLaunchStartsAPrivateClientThroughItsPublishedBinding pins runContainer's call site,
// which no unit test can drive (it starts a real container), in the style of
// TestTheFreshLaunchRunsTheJailAsAHoldAndItsFirstSessionByExec: after the keeper has published
// its entrypoints record (startKeeper) and before the first session runs (runArmedSession), the
// target command is bound (freshPrivateClientCommand) and, when it changed, the first session's
// exec is rebuilt from the bound command. Deleting either fails here.
func TestTheFreshLaunchStartsAPrivateClientThroughItsPublishedBinding(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	pos := map[string]token.Pos{}
	rebuilt := false
	ast.Inspect(fd, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if assign, ok := n.(*ast.AssignStmt); ok && len(assign.Lhs) == 1 && skelIdent(assign.Lhs[0]) == "firstExec" && assign.Tok == token.ASSIGN {
			if call, ok := assign.Rhs[0].(*ast.CallExpr); ok && skelCallee(call) == "firstSessionExecCmd" && len(call.Args) == 4 {
				if inner, ok := call.Args[2].(*ast.CallExpr); ok && skelCallee(inner) == "sessionCmd" && len(inner.Args) == 1 && skelIdent(inner.Args[0]) == "bound" {
					rebuilt = true
				}
			}
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch name := skelCallee(call); name {
		case "startKeeper", "freshPrivateClientCommand", "runArmedSession":
			if _, seen := pos[name]; !seen {
				pos[name] = call.Pos()
			}
			if name == "freshPrivateClientCommand" && (len(call.Args) != 4 || skelIdent(call.Args[3]) != "targetCmd") {
				t.Errorf("freshPrivateClientCommand binds %v, want the launch's targetCmd", call.Args)
			}
		}
		return true
	})
	last := token.NoPos
	for _, name := range []string{"startKeeper", "freshPrivateClientCommand", "runArmedSession"} {
		p, ok := pos[name]
		if !ok {
			t.Fatalf("runContainer no longer calls %s", name)
		}
		if p < last {
			t.Errorf("%s is out of order in runContainer", name)
		}
		last = p
	}
	if !rebuilt {
		t.Error("runContainer does not rebuild the first session's exec from the bound command")
	}
}

// TestTheFreshLaunchStartsEachCheckedPrivateClientsFront pins runContainer's call site for the
// front daemons (slice 3.4), which no unit test can drive: after prepareOrdinaryPrograms has kept
// the checked deliveries, privateHTTPFrontDaemons of them is appended to the jailDaemons the run
// command is assembled from. Deleting the append fails here.
func TestTheFreshLaunchStartsEachCheckedPrivateClientsFront(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	prepare, front := token.NoPos, token.NoPos
	ast.Inspect(fd, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && skelCallee(call) == "prepareOrdinaryPrograms" && prepare == token.NoPos {
			prepare = call.Pos()
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || skelIdent(assign.Lhs[0]) != "jailDaemons" || len(assign.Rhs) != 1 {
			return true
		}
		ast.Inspect(assign.Rhs[0], func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok || skelCallee(call) != "privateHTTPFrontDaemons" || len(call.Args) != 1 {
				return true
			}
			if sel, ok := call.Args[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "privateClientEntrypoints" {
				front = call.Pos()
			}
			return true
		})
		return true
	})
	if prepare == token.NoPos {
		t.Fatal("runContainer no longer calls prepareOrdinaryPrograms")
	}
	if front == token.NoPos {
		t.Fatal("runContainer does not append privateHTTPFrontDaemons(o.privateClientEntrypoints) to jailDaemons")
	}
	if front < prepare {
		t.Error("the front daemons are composed before prepareOrdinaryPrograms keeps the checked deliveries")
	}
}

// A running jail whose keeper recorded a checked private client, with no committed binding left
// for it, refuses the attach: its ordinary launcher would otherwise run the client.
func TestAttachRefusesACheckedPrivateClientWithNoCommittedBinding(t *testing.T) {
	for _, tc := range []struct {
		name       string
		writeInput bool
		recorded   []string
		want       bool
	}{
		{"recorded client, no launch state", false, []string{"fixture-client"}, false},
		{"recorded client, binding committed", true, []string{"fixture-client"}, true},
		{"no recorded client (unselected front)", false, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := attachedPrivateHTTPFixture(t, true, tc.writeInput)
			rec, ok := readKeeperRecord("yolo-ws-abcd1234")
			if !ok {
				rec = keeperRecord{Workspace: f.o.Workspace, Runtime: "podman", PackTree: f.tree, MCPTeardownKnown: true}
			}
			rec.PrivateClients = tc.recorded
			writeCurrentKeeperRecord(t, "yolo-ws-abcd1234", rec)
			rc, argv := attachRecordingArgs(t, f, "fixture-client --version")
			if !tc.want {
				out := f.stderr.String()
				if rc != 1 || argv != nil || !strings.Contains(out, "fixture-client") || !strings.Contains(out, "no committed binding") || !strings.Contains(out, "repeat the launch") {
					t.Fatalf("attach ran a checked client with no binding: rc=%d argv=%q\n%s", rc, argv, out)
				}
				return
			}
			if rc != 0 || argv == nil {
				t.Fatalf("attach refused: rc=%d\n%s", rc, f.stderr.String())
			}
		})
	}
}

// A private client the bootstrap would start without its pack's launch flags is refused, before
// anything is prepared, rather than run with them silently dropped.
func TestPrepareOrdinaryProgramsRefusesAPrivateClientWithLaunchFlags(t *testing.T) {
	oldHost := hostPlatform
	t.Cleanup(func() { hostPlatform = oldHost })
	hostPlatform = privateClientPlatform
	if containerJailPlatform() != privateClientPlatform {
		t.Skip("the private client cell is linux/amd64 only")
	}
	repoRoot := t.TempDir()
	for _, name := range []string{"flake.nix", "flake.lock"} {
		if err := os.WriteFile(filepath.Join(repoRoot, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f := attachedPrivateHTTPFixture(t, true, false)
	flagsDir := filepath.Join(t.TempDir(), "flags")
	if err := os.Mkdir(flagsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flagsDir, "pack.json"), []byte(`{"name":"flags","contributes":[{"kind":"autonomy","autonomous":{"launch":[{"bin":"fixture-client","flags":["--fixture-flag"]}]}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	flags, problems := packload.LoadDir(flagsDir, "flags")
	if flags == nil || len(problems) != 0 {
		t.Fatalf("launch-flag pack: %v", problems)
	}
	o := f.o
	var stderr strings.Builder
	o.Stderr = &stderr
	o.approvedHTTPBindings = map[string]config.ApprovedCallBrokerHTTP{"tasks": {Pack: "front-pack"}}
	calls := 0
	o.PreparePrograms = func(ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
		calls++
		return nil, nil
	}
	rc := o.prepareOrdinaryPrograms(jsonx.NewOrderedMap(), "podman", repoRoot, append(f.packs, flags), nil)
	out := stderr.String()
	if rc != 1 || calls != 0 {
		t.Fatalf("a private client with launch flags was prepared: rc=%d calls=%d\n%s", rc, calls, out)
	}
	for _, want := range []string{"fixture-client", "launch flags", "Repair:"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal does not name %q:\n%s", want, out)
		}
	}
}

// TestTheKeepersStartRecordNamesItsCheckedPrivateClients: the start record a later attach reads
// (TestAttachRefusesACheckedPrivateClientWithNoCommittedBinding) names every private client the
// launch checked, so an attach can tell a checked client from none.
func TestTheKeepersStartRecordNamesItsCheckedPrivateClients(t *testing.T) {
	checked := checkedPrivateClientFixture("semver", "audited-semver", "http", "audited-semver", strings.Repeat("a", 64))
	var first *sessionLock
	f := startKeeperFixtureWith(t, true, func(p *keeperPlan) {
		p.PrivateClientEntrypoints = []callbrokerinput.PreparedHTTPClientEntrypoint{checked}
		// sh -c takes the binding pair inserted before the image as positional words it ignores.
		p.RunCmd = append(p.RunCmd, p.ImageRef)
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		first = lock
	}, func(o *Options) {
		b := checked.Binding
		b.Generation = strings.Repeat("a", 32)
		o.privateClientBindings = []entrypoint.PrivateClientBinding{b}
		// The owner that published the generation's entrypoints record names its front, whose
		// generation the keeper hands the container beside the binding (slice 3.4).
		published, err := os.Stat(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		o.preparedHTTPGenerationOwners = map[string]preparedHTTPGenerationOwner{"fixture": {path: "fixture", generation: b.Generation,
			entrypointsFile: published, entrypoints: callbrokerinput.PreparedHTTPClientEntrypoints{Loophole: "http"}}}
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	rec, ok := readKeeperRecord(f.cname)
	if !ok || !slices.Equal(rec.PrivateClients, []string{"semver"}) {
		t.Errorf("the start record names private clients %v (%v), want [semver]", rec.PrivateClients, ok)
	}
	first.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
}

// TestPrepareOrdinaryProgramsRefusesAPrivateClientAMiseToolNames: a declared mise tool whose
// binary is a selected private client's bin would hand that name to mise's ordinary copy, so the
// launch refuses before preparation and names the repair, whatever backend prefix the key has.
func TestPrepareOrdinaryProgramsRefusesAPrivateClientAMiseToolNames(t *testing.T) {
	oldHost := hostPlatform
	t.Cleanup(func() { hostPlatform = oldHost })
	hostPlatform = privateClientPlatform
	if containerJailPlatform() != privateClientPlatform {
		t.Skip("the private client cell is linux/amd64 only")
	}
	for _, key := range []string{"npm:fixture-client", "fixture-client", "aqua:owner/fixture-client"} {
		t.Run(key, func(t *testing.T) {
			repoRoot := t.TempDir()
			for _, name := range []string{"flake.nix", "flake.lock"} {
				if err := os.WriteFile(filepath.Join(repoRoot, name), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			f := attachedPrivateHTTPFixture(t, true, false)
			o := f.o
			var stderr strings.Builder
			o.Stderr = &stderr
			o.approvedHTTPBindings = map[string]config.ApprovedCallBrokerHTTP{"tasks": {Pack: "front-pack"}}
			calls := 0
			o.PreparePrograms = func(ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
				calls++
				return nil, nil
			}
			cfg := jsonx.NewOrderedMap()
			install, tools := jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
			tools.Set(key, "1.0.0")
			install.Set("mise", tools)
			cfg.Set("install", install)
			rc := o.prepareOrdinaryPrograms(cfg, "podman", repoRoot, f.packs, nil)
			out := stderr.String()
			if rc != 1 || calls != 0 {
				t.Fatalf("a private client a mise tool names was prepared: rc=%d calls=%d\n%s", rc, calls, out)
			}
			for _, want := range []string{"fixture-client", key, "mise", "Repair:"} {
				if !strings.Contains(out, want) {
					t.Errorf("refusal does not name %q:\n%s", want, out)
				}
			}
		})
	}
}
