package run

// jailgrant_test.go pins --with-credentials AT A JAIL LAUNCH (jailgrant.go;
// docs/design/credential-sources-separation.md OQ-ES5's jail half, ES-D31 to ES-D39), one cell per
// vehicle and per rule, each driven through the production path that carries it: Run for the
// resolution and the macos-user arm, a whole podman launch to its keeper's plan, the grant file
// staging on Apple Container, the keeper's start record and its teardown, and attachExisting for
// the attach rule. The front door's parse is internal/cli's jailwithcredentials_test.go.

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// grantConfig is a user config selecting packs (a JSON list's members) and rest (more members,
// or ""), whose env_sources is the one store every cell launches with: a DOTENV FILE holding two
// providers' keys and one value no provider claims. A file, not an inline map, so the merged
// config the launch freezes into the workspace (config-assembled.json) holds no value of its own,
// and a value found in a workspace file is the grant's doing.
func grantConfig(t *testing.T, packs, rest string) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "creds.env")
	if err := os.WriteFile(store, []byte("ZAI_API_KEY=tok-z\nCEREBRAS_API_KEY=tok-c\nPORT=8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	quoted, _ := json.Marshal(store)
	if rest != "" {
		rest = ", " + rest
	}
	return `{"packs": [` + packs + `], "env_sources": [` + string(quoted) + `]` + rest + `}`
}

// grantValues are the store's credential values, which no disclosure, argv, record or workspace
// file may carry.
var grantValues = []string{"tok-z", "tok-c"}

// filesHolding lists every regular file under root whose bytes contain value.
func filesHolding(t *testing.T, root, value string) []string {
	t.Helper()
	var hits []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if b, rerr := os.ReadFile(p); rerr == nil && bytes.Contains(b, []byte(value)) {
			hits = append(hits, p)
		}
		return nil
	})
	return hits
}

// macosUserGrantRun drives Run on macos-user with the typed grant, returning the launch env the
// backend was handed (nil when the launch never reached it), the exit code and the streams.
func macosUserGrantRun(t *testing.T, ws, config string, args, grant []string,
	profiles map[string]string) (*jsonx.OrderedMap, int, string) {
	t.Helper()
	home := packHome(t)
	writeUserConfig(t, home, config)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = args
	o.UseProfiles = profiles
	o.WithCredentials = grant
	var got *jsonx.OrderedMap
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, packEnv *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		got = packEnv
		return 0
	}
	rc := Run(*o)
	return got, rc, stdout.String() + stderr.String()
}

// MACOS-USER: the grant rides the session's launch env, which the backend writes into the
// root-owned per-session env file, and NEVER the per-agent env files the arm writes under
// <workspace>/.yolo/home: claude, on its zai profile and granted cerebras, holds both keys in its
// session, its own env file holds the profile's key and not the granted one, and no file of the
// workspace holds the granted value. Keeping the grant out of the channel is what this pins: fed
// to the gate as a recipient (the host's ScopeInput.Grants), claude's env file carries
// CEREBRAS_API_KEY, the leak the audit measured.
func TestMacosUserGrantRidesTheSessionEnvFileOnly(t *testing.T) {
	ws := t.TempDir()
	env, rc, out := macosUserGrantRun(t, ws, grantConfig(t, `"claude", "zai", "cerebras"`, ""),
		[]string{"claude"}, []string{"cerebras"}, map[string]string{"claude": "zai"})
	if rc != 0 || env == nil {
		t.Fatalf("Run() = %d, launch env handed = %v\n%s", rc, env != nil, out)
	}
	for k, want := range map[string]string{"CEREBRAS_API_KEY": "tok-c", "ZAI_API_KEY": "tok-z", "PORT": "8080"} {
		if got := envAt(env, k); got != want {
			t.Errorf("the session's launch env: %s = %q, want %q", k, got, want)
		}
	}
	if !strings.Contains(envAt(env, "ANTHROPIC_BASE_URL"), "z.ai") {
		t.Errorf("claude keeps its zai profile beside the grant: ANTHROPIC_BASE_URL = %q",
			envAt(env, "ANTHROPIC_BASE_URL"))
	}
	if hits := filesHolding(t, paths.WorkspaceHomeState(ws), "tok-c"); len(hits) != 0 {
		t.Errorf("the granted value reached a file under <workspace>/.yolo/home: %v", hits)
	}
	claudeFile := filepath.Join(paths.WorkspaceHomeState(ws), macosUserAgentEnvDir, "claude.sh")
	if b, err := os.ReadFile(claudeFile); err != nil || !strings.Contains(string(b), "tok-z") {
		t.Errorf("claude's own env file must still carry its profile's key (err %v):\n%s", err, b)
	}
	for _, want := range []string{
		"Credential grant (--with-credentials cerebras): this session receives the granted providers' " +
			"claimed env_sources values, keys only",
		"and claude and everything it starts inherit them",
		"each agent keeps its profile, and the grant only adds keys beside it",
		"  cerebras: CEREBRAS_API_KEY",
		"CEREBRAS_API_KEY (provider cerebras): every process of this session, by its " +
			"--with-credentials grant",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the macos-user launch must disclose %q:\n%s", want, out)
		}
	}
	for _, v := range grantValues {
		if strings.Contains(out, v) {
			t.Errorf("the launch printed a credential VALUE (%s):\n%s", v, out)
		}
	}
}

// The resolution is above the dispatch: an unknown provider refuses before either arm starts a
// thing, in the host's words, naming every known provider; a named provider env_sources holds no
// value for is reported, never skipped; and `all` is every provider claiming a value.
func TestAJailGrantResolvesAsTheHostsDoes(t *testing.T) {
	cfg := grantConfig(t, `"zai", "cerebras", "kilo"`, "")
	env, rc, out := macosUserGrantRun(t, t.TempDir(), cfg, []string{"bash"}, []string{"zai", "zia"}, nil)
	if rc != 1 || env != nil {
		t.Errorf("--with-credentials zia must refuse before the backend: rc = %d, reached = %v\n%s", rc, env != nil, out)
	}
	for _, want := range []string{"Refusing to launch", `"zia"`, "cerebras, kilo, zai", "`all`"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal must carry %q:\n%s", want, out)
		}
	}

	env, rc, out = macosUserGrantRun(t, t.TempDir(), cfg, []string{"bash"}, []string{"zai", "kilo"}, nil)
	if rc != 0 || envAt(env, "ZAI_API_KEY") != "tok-z" || envAt(env, "CEREBRAS_API_KEY") != "" {
		t.Errorf("--with-credentials zai,kilo: rc %d, ZAI %q, CEREBRAS %q\n%s", rc,
			envAt(env, "ZAI_API_KEY"), envAt(env, "CEREBRAS_API_KEY"), out)
	}
	if want := "  kilo: nothing granted — env_sources holds no value for the names it claims (KILO_API_KEY)"; !strings.Contains(out, want) {
		t.Errorf("a named provider with no value must be reported (%q):\n%s", want, out)
	}

	env, rc, out = macosUserGrantRun(t, t.TempDir(), cfg, []string{"bash"}, []string{"all"}, nil)
	if rc != 0 || envAt(env, "ZAI_API_KEY") != "tok-z" || envAt(env, "CEREBRAS_API_KEY") != "tok-c" {
		t.Errorf("--with-credentials all: rc %d, ZAI %q, CEREBRAS %q\n%s", rc,
			envAt(env, "ZAI_API_KEY"), envAt(env, "CEREBRAS_API_KEY"), out)
	}
	if strings.Contains(out, "  kilo:") {
		t.Errorf("kilo claims no value env_sources holds, so `all` does not name it:\n%s", out)
	}
}

// NOTHING ELSE IMPLIES IT, and config cannot express it: a profile, a -p, every YOLO_ALLOW_* hatch
// and an environment spelling of the flag leave an unnamed provider's key out of a shell's
// session and print no grant; a config key spelling the grant is refused as the unknown key it is.
func TestAJailGrantIsImpliedByNothingElse(t *testing.T) {
	for _, k := range []string{"YOLO_WITH_CREDENTIALS", "YOLO_ALLOW_ALL_CREDENTIALS", "YOLO_ALLOW_MISSING_PROVIDERS"} {
		t.Setenv(k, "1")
	}
	cfg := grantConfig(t, `"claude", "zai", "cerebras"`, `"profile": {"claude": "zai"}`)
	env, rc, out := macosUserGrantRun(t, t.TempDir(), cfg, []string{"bash"}, nil, map[string]string{"claude": "zai"})
	if rc != 0 || env == nil {
		t.Fatalf("Run() = %d\n%s", rc, out)
	}
	for _, k := range []string{"ZAI_API_KEY", "CEREBRAS_API_KEY"} {
		if v := envAt(env, k); v != "" {
			t.Errorf("no --with-credentials was typed, yet a shell's session holds %s=%q", k, v)
		}
	}
	if strings.Contains(out, "Credential grant") {
		t.Errorf("no --with-credentials was typed, yet a grant was disclosed:\n%s", out)
	}
	for _, key := range []string{"with_credentials", "credentials"} {
		env, rc, out = macosUserGrantRun(t, t.TempDir(),
			grantConfig(t, `"zai"`, `"`+key+`": ["zai"]`), []string{"bash"}, nil, nil)
		if rc == 0 || env != nil || !strings.Contains(out, "config."+key+": unknown key") {
			t.Errorf("config key %q must be refused as unknown, never read as a grant: rc %d, reached %v\n%s",
				key, rc, env != nil, out)
		}
	}
}

// PODMAN, through a whole fresh launch to its keeper's plan (ES-D37): the container argv carries
// no granted name or value as `-e`, only a `:ro` bind of the launch's grant file at the jail
// home's grant path; that file is the launcher's own, 0600 in a 0700 directory outside the
// workspace, and holds exactly the granted value; the plan holds no value at all; nothing the
// launch wrote into the workspace holds one; and a launch whose container never started takes its
// grant file back.
func TestAPodmanJailGrantRidesAGrantFileNeverTheArgv(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, grantConfig(t, `"zai", "cerebras"`, ""))
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	hostFile := jailGrantHostFile(cname)
	var plan *keeperPlan
	var rawPlan, atSpawn []byte
	var fileMode, dirMode os.FileMode
	saved := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	defaultKeeperSpawner = func(_ *Options, planPath string, _, _, _ *os.File, _ []*os.File) (func() int, error) {
		rawPlan, _ = os.ReadFile(planPath)
		atSpawn, _ = os.ReadFile(hostFile)
		if fi, err := os.Stat(hostFile); err == nil {
			fileMode = fi.Mode().Perm()
		}
		if fi, err := os.Stat(filepath.Dir(hostFile)); err == nil {
			dirMode = fi.Mode().Perm()
		}
		p, err := readKeeperPlan(planPath)
		if err != nil {
			return nil, err
		}
		plan = p
		return func() int { return 3 }, nil
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.Args = []string{"bash"}
	o.WithCredentials = []string{"zai"}
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	_ = Run(*o)
	out := stdout.String() + stderr.String()
	if plan == nil {
		t.Fatalf("the launch never reached its keeper's spawn:\n%s", out)
	}
	bind := hostFile + ":/home/agent/" + entrypoint.JailGrantFileRel + ":ro"
	if i := slices.Index(plan.RunCmd, bind); i < 1 || plan.RunCmd[i-1] != "-v" {
		t.Errorf("the container argv must bind the grant file `-v %s`: %q", bind, plan.RunCmd)
	}
	for i, w := range plan.RunCmd {
		for _, v := range grantValues {
			if strings.Contains(w, v) {
				t.Errorf("the container argv carries a credential value in %q", w)
			}
		}
		if i > 0 && plan.RunCmd[i-1] == "-e" && (strings.HasPrefix(w, "ZAI_API_KEY") || strings.HasPrefix(w, "CEREBRAS_API_KEY")) {
			t.Errorf("a granted name rides the argv as `-e %s`, which podman resolves into the container's "+
				"configuration and database", w)
		}
	}
	if want := "export ZAI_API_KEY='tok-z'\n"; !strings.Contains(string(atSpawn), want) ||
		strings.Contains(string(atSpawn), "tok-c") {
		t.Errorf("the grant file at the spawn held %q, want the zai key and no other", atSpawn)
	}
	if fileMode != 0o600 || dirMode != 0o700 {
		t.Errorf("the grant file is %o in a %o directory, want 0600 in 0700", fileMode, dirMode)
	}
	if strings.HasPrefix(hostFile, ws) {
		t.Errorf("the grant file %s is inside the workspace", hostFile)
	}
	for _, v := range grantValues {
		if strings.Contains(string(rawPlan), v) {
			t.Errorf("the keeper's plan holds a credential value (%s)", v)
		}
	}
	if plan.Grant == nil || !slices.Equal(plan.Grant.Providers, []string{"zai"}) {
		t.Fatalf("the plan's grant (for the keeper's start record) = %+v, want the zai grant", plan.Grant)
	}
	if hits := filesHolding(t, ws, "tok-z"); len(hits) != 0 {
		t.Errorf("the granted value reached a file in the workspace: %v", hits)
	}
	if _, err := os.Stat(hostFile); !os.IsNotExist(err) {
		t.Errorf("a launch whose container never started left its grant file %s (%v)", hostFile, err)
	}
	for _, want := range []string{
		"Credential grant (--with-credentials zai): this jail holds the granted providers' claimed " +
			"env_sources values for its whole life, keys only",
		"every process in it inherits them: this session, every session attached to it later, and " +
			"everything each one starts",
		"  zai: ZAI_API_KEY",
		"ZAI_API_KEY (provider zai): every process in this jail, by its --with-credentials grant",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the fresh launch must disclose %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ZAI_API_KEY (provider zai): withheld") {
		t.Errorf("a granted key is not withheld from any process:\n%s", out)
	}
	for _, v := range grantValues {
		if strings.Contains(out, v) {
			t.Errorf("the launch printed a credential VALUE (%s):\n%s", v, out)
		}
	}
}

// APPLE CONTAINER (ES-D37): no granted name or value on the argv, and no bind either, below or
// above its `:ro` floor; the grant file is copied into the jail home the backend binds whole, 0600,
// beside the launcher's own host copy, which an attach reads.
func TestAnAppleContainerJailGrantIsAFileInTheJailHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	packs := []*packload.Pack{officialPack(t, "zai"), officialPack(t, "cerebras")}
	wsState := t.TempDir()
	in := relocationInput(t, "container", wsState, nil)
	in.packs = packs
	store := jsonx.NewOrderedMap()
	store.Set("ZAI_API_KEY", "tok-z")
	store.Set("CEREBRAS_API_KEY", "tok-c")
	in.channel = channelFor(t, o, in.cfg, packs, store)
	o.WithCredentials = []string{"cerebras"}
	if err := o.resolveJailGrant(in.channel); err != nil {
		t.Fatal(err)
	}
	if err := o.stageJailGrant(in.cname, "container", wsState); err != nil {
		t.Fatal(err)
	}
	argv := o.assembleRunCmd(in)
	for _, w := range argv {
		if strings.Contains(w, "tok-") || strings.HasPrefix(w, "CEREBRAS_API_KEY") || strings.Contains(w, "yolo-grant-env") {
			t.Errorf("Apple Container's argv carries %q", w)
		}
	}
	inHome := filepath.Join(wsState, entrypoint.JailGrantFileRel)
	b, err := os.ReadFile(inHome)
	if err != nil || !strings.Contains(string(b), "export CEREBRAS_API_KEY='tok-c'\n") || strings.Contains(string(b), "tok-z") {
		t.Errorf("the jail home's grant file = %q (%v), want the cerebras key alone", b, err)
	}
	if fi, err := os.Stat(inHome); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the jail home's grant file mode: %v %v, want 0600", fi, err)
	}
	if got := grantFileValues(jailGrantHostFile(in.cname)); got["CEREBRAS_API_KEY"] != "tok-c" || len(got) != 1 {
		t.Errorf("the host copy holds %v, want the cerebras key alone", got)
	}
}

// THE KEEPER records the grant's names, never a value, in its start record for the attach to read,
// and hands the container's client no granted value: the values are in the grant file alone.
func TestTheKeeperRecordsTheGrantByNameAndHandsTheClientNoValue(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	grant := &jailGrant{Spelled: "zai", Providers: []string{"zai"},
		Granted: []grantedProvider{{Provider: "zai", Delivered: []string{"ZAI_API_KEY"}, Claims: []string{"ZAI_API_KEY"}}}}
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
		p.Grant = grant
		p.RunCmd = []string{"sh", "-c", `printf '[%s]' "${ZAI_API_KEY-}" > ` + shquote.Quote(seen) + "; " + p.RunCmd[2]}
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	if b, err := os.ReadFile(seen); err != nil || string(b) != "[]" {
		t.Errorf("the container's client environment carries ZAI_API_KEY = %q (err %v): no value may cross "+
			"to the runtime, whose configuration and database keep what its client hands it", b, err)
	}
	rec, ok := readKeeperRecord(f.cname)
	if !ok || rec.Grant == nil || !rec.Grant.holds("ZAI_API_KEY") || rec.Grant.Spelled != "zai" {
		t.Errorf("the start record's grant = %+v (%v), want the zai grant by name", rec.Grant, ok)
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
}

// THE JAIL'S TEARDOWN TAKES ITS CREDENTIAL FILES WITH IT (ES-D38), through a keeper's normal end:
// once the last session leaves and the container is known gone, the grant file (the host copy and
// the mountpoint in the home) and every per-agent env file are removed, and the per-agent directory
// the next launch binds stays. Before, a rotated key's launch-time value an attach's agent file
// named outlived the jail until the workspace's next entry.
func TestTheJailsTeardownRemovesItsGrantAndAgentEnvFiles(t *testing.T) {
	var session *sessionLock
	var ws, wsState, cname string
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		ws = p.Workspace
		cname = yoloruntime.FromWorkspace(ws)
		p.Cname = cname
		wsState = paths.WorkspaceHomeState(ws)
		writeExec(t, filepath.Join(wsState, agentEnvStateDir, "pi.sh"), "export ZAI_API_KEY='tok-old'\n")
		writeExec(t, filepath.Join(wsState, jailGrantHomeRel("podman")), "")
		writeExec(t, jailGrantHostFile(cname), "export ZAI_API_KEY='tok-old'\n")
		lock, _, err := takeSessionLock(cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Fatalf("the keeper ended %d", rc)
	}
	for _, gone := range []string{filepath.Join(wsState, agentEnvStateDir, "pi.sh"),
		filepath.Join(wsState, jailGrantHomeRel("podman")), filepath.Dir(jailGrantHostFile(cname))} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("the jail's teardown left %s (%v)", gone, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(wsState, agentEnvStateDir)); err != nil || !fi.IsDir() {
		t.Errorf("the teardown removed the per-agent directory the next launch binds: %v", err)
	}
}

// THE ATTACH, through the real attachExisting against a running jail whose keeper's start record
// names what it was launched with: the same set, a subset, or no request enters, and the session is
// told what it holds; a provider or a name the jail was not launched with is refused before
// anything is written, naming the fresh launch.
func TestAnAttachHoldsTheJailsGrantAndAsksForNoOther(t *testing.T) {
	zaiOnly := &jailGrant{Spelled: "zai", Providers: []string{"zai"},
		Granted: []grantedProvider{{Provider: "zai", Delivered: []string{"ZAI_API_KEY"}, Claims: []string{"ZAI_API_KEY"}}}}
	both := &jailGrant{Spelled: "zai,cerebras", Providers: []string{"cerebras", "zai"},
		Granted: []grantedProvider{
			{Provider: "cerebras", Delivered: []string{"CEREBRAS_API_KEY"}, Claims: []string{"CEREBRAS_API_KEY"}},
			{Provider: "zai", Delivered: []string{"ZAI_API_KEY"}, Claims: []string{"ZAI_API_KEY"}}}}
	for _, tc := range []struct {
		name    string
		running *jailGrant
		request []string
		enters  bool
		want    []string
		not     []string
	}{
		{"the same set", zaiOnly, []string{"zai"}, true,
			[]string{"Credential grant (this jail was launched with --with-credentials zai)",
				"this session and everything it starts inherit them", "asks for nothing more", "  zai: ZAI_API_KEY",
				"ZAI_API_KEY (provider zai): every process in this jail, by its --with-credentials grant"}, nil},
		{"a subset", both, []string{"cerebras"}, true,
			[]string{"this jail was launched with --with-credentials zai,cerebras", "  cerebras: CEREBRAS_API_KEY",
				"CEREBRAS_API_KEY (provider cerebras): every process in this jail"}, nil},
		{"no request", zaiOnly, nil, true,
			[]string{"Credential grant (this jail was launched with --with-credentials zai)"},
			[]string{"asks for nothing more"}},
		{"a provider the jail lacks", zaiOnly, []string{"cerebras"}, false,
			[]string{"Refusing to attach", "asks for --with-credentials cerebras",
				"launched with --with-credentials zai", "cerebras (CEREBRAS_API_KEY)",
				"'yolo stop' from this workspace", "`yolo --with-credentials zai,cerebras -- claude` (the jail's grant",
				// ES-D39: the grant's rule, and no claim the jail takes no other credential, since an
				// attach's -p delivers its profile's key, as ruled ("OQ-ES5 (attach -p)").
				"--with-credentials grant is fixed when the jail is launched, and an attach cannot add to it"},
			[]string{"Attaching to", "takes no others", "holds exactly the credentials"}},
		{"a jail launched with no grant", nil, []string{"zai"}, false,
			[]string{"Refusing to attach", "launched with no --with-credentials grant", "`yolo --with-credentials zai -- claude`"},
			[]string{"the jail's grant and this entry's together"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packs := append(zaiSelected(t), officialPack(t, "cerebras"))
			store := jsonx.NewOrderedMap()
			store.Set("ZAI_API_KEY", "tok-z")
			store.Set("CEREBRAS_API_KEY", "tok-c")
			o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, store, nil)
			o.Args = []string{"claude"}
			o.WithCredentials = tc.request
			if err := o.resolveJailGrant(channel); err != nil {
				t.Fatal(err)
			}
			// The jail's keeper, alive (its liveness lock held) with its start record naming the
			// grant, and another session in the jail, so this one's quit leaves it up.
			const cname = "yolo-ws-abcd1234"
			live, err := holdLivenessLock(cname)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseLock(live)
			other, _, err := takeSessionLock(cname)
			if err != nil {
				t.Fatal(err)
			}
			defer other.release()
			if err := writeKeeperRecord(cname, keeperRecord{PID: 4242, Started: time.Now(),
				Grant: tc.running}); err != nil {
				t.Fatal(err)
			}
			envFile, before := seedLiveChannelFile(t, o)
			rc, restarted, execed := attachToExec(t, o, cfg, packs, channel)
			out := stderr.String()
			if tc.enters != (rc == 0 && execed) || restarted {
				t.Fatalf("enters = %v, want %v: rc %d restarted %v execed %v\n%s", rc == 0 && execed, tc.enters,
					rc, restarted, execed, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("the attach must say %q:\n%s", want, out)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(out+o.Stdout.(*bytes.Buffer).String(), not) {
					t.Errorf("the attach must not say %q:\n%s", not, out)
				}
			}
			if !tc.enters {
				if after, _ := os.ReadFile(envFile); !bytes.Equal(after, before) {
					t.Errorf("a refused attach rewrote the live channel file")
				}
			}
			for _, v := range grantValues {
				if strings.Contains(out, v) {
					t.Errorf("the attach printed a credential VALUE (%s):\n%s", v, out)
				}
			}
		})
	}
}

// AN ATTACH READS THE JAIL'S GRANT FILE AS YOLO'S VALUES (ES-D36, ES-D37): every session's boot
// puts the granted values in its environment from the grant file, not from the container's frozen
// environment, so the attach adds them to what it knows yolo set (bootEnv). An agent whose profile
// selects the granted provider then overrides the launch-time value with its profile's current one
// (the `case` guard), as it did when the values were frozen into the container, rather than
// deferring to the stale grant. A profile at an attach delivers that way, as the design's
// "OQ-ES5 (attach -p)" ledger row rules (ES-D39).
func TestAnAttachTreatsTheGrantFilesValuesAsYolos(t *testing.T) {
	packs := zaiSelected(t)
	store := jsonx.NewOrderedMap()
	store.Set("ZAI_API_KEY", "tok-new")
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, store,
		func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })
	const cname = "yolo-ws-abcd1234"
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	other, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer other.release()
	grant := &jailGrant{Spelled: "zai", Providers: []string{"zai"},
		Granted: []grantedProvider{{Provider: "zai", Delivered: []string{"ZAI_API_KEY"}, Claims: []string{"ZAI_API_KEY"}}}}
	if err := writeKeeperRecord(cname, keeperRecord{PID: 4242, Started: time.Now(), Grant: grant}); err != nil {
		t.Fatal(err)
	}
	writeExec(t, jailGrantHostFile(cname), "export ZAI_API_KEY='tok-old'\n")
	if rc, _, execed := attachToExec(t, o, cfg, packs, channel); rc != 0 || !execed {
		t.Fatalf("attach rc %d execed %v\n%s", rc, execed, stderr.String())
	}
	b, err := os.ReadFile(filepath.Join(paths.WorkspaceHomeState(o.Workspace), agentEnvStateDir, "claude.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `case "${ZAI_API_KEY-}" in ''|'tok-old') export ZAI_API_KEY='tok-new' ;; esac`; !strings.Contains(string(b), want) {
		t.Errorf("claude's file must override the grant's launch-time value with its profile's (%q):\n%s", want, b)
	}
	// The grant names zai, so the profile delivers nothing beyond it, and the block says nothing more.
	if strings.Contains(stderr.String(), "beyond the grant") {
		t.Errorf("a profile delivering only granted keys was disclosed as going beyond the grant:\n%s", stderr.String())
	}
}

// AN ATTACH'S PROFILE BRINGS ITS PROVIDER'S KEY, AND THE GRANT BLOCK SAYS SO (the design's
// "OQ-ES5 (attach -p)" ledger row, ruled 2026-10-05; ES-D39): a jail launched with
// --with-credentials cerebras, attached with claude on the zai profile, hands claude ZAI_API_KEY
// in its own env file, which the grant did not name, and the attach's grant disclosure names the
// agent, the profile and the key, never the value. Deleting the call that adds the line, or the
// line itself, fails it.
func TestAnAttachSaysWhenItsProfileDeliversAKeyTheGrantDidNotName(t *testing.T) {
	packs := append(zaiSelected(t), officialPack(t, "cerebras"))
	store := jsonx.NewOrderedMap()
	store.Set("ZAI_API_KEY", "tok-z")
	store.Set("CEREBRAS_API_KEY", "tok-c")
	o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, store,
		func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })
	const cname = "yolo-ws-abcd1234"
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	other, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer other.release()
	grant := &jailGrant{Spelled: "cerebras", Providers: []string{"cerebras"},
		Granted: []grantedProvider{{Provider: "cerebras", Delivered: []string{"CEREBRAS_API_KEY"}, Claims: []string{"CEREBRAS_API_KEY"}}}}
	if err := writeKeeperRecord(cname, keeperRecord{PID: 4242, Started: time.Now(), Grant: grant}); err != nil {
		t.Fatal(err)
	}
	if rc, _, execed := attachToExec(t, o, cfg, packs, channel); rc != 0 || !execed {
		t.Fatalf("attach rc %d execed %v\n%s", rc, execed, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"Credential grant (this jail was launched with --with-credentials cerebras)",
		"  and beyond the grant: claude's zai profile, selected by this entry, delivers ZAI_API_KEY into " +
			"claude's own env file, which every process of this jail can read",
		"OQ-ES5 (attach -p)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the attach's grant disclosure must say %q:\n%s", want, out)
		}
	}
	for _, v := range grantValues {
		if strings.Contains(out, v) {
			t.Errorf("the attach printed a credential VALUE (%s):\n%s", v, out)
		}
	}
	// The behavior the ruling keeps: claude's own file carries the profile's key.
	b, _ := os.ReadFile(filepath.Join(paths.WorkspaceHomeState(o.Workspace), agentEnvStateDir, "claude.sh"))
	if !strings.Contains(string(b), "tok-z") {
		t.Errorf("an attach's profile still delivers its provider's key, by ruling; claude's file:\n%s", b)
	}
}

// A LAUNCH REFUSED AFTER IT STAGED ITS GRANT FILE, AND BEFORE ANY KEEPER, TAKES THE FILE BACK
// (ES-D38's discardUnheldJailGrant): the credential pre-flight refuses claude's zai profile with no
// zai key after the grant file for cerebras is written, and no teardown runs for a container that
// never existed, so Run's deferred discard is the only thing that removes it.
func TestALaunchRefusedAfterStagingItsGrantRemovesTheFile(t *testing.T) {
	home := packHome(t)
	store := filepath.Join(t.TempDir(), "creds.env")
	if err := os.WriteFile(store, []byte("CEREBRAS_API_KEY=tok-c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	quoted, _ := json.Marshal(store)
	writeUserConfig(t, home, `{"packs": ["claude", "zai", "cerebras"], "env_sources": [`+string(quoted)+`]}`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	saved := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	spawned := false
	defaultKeeperSpawner = func(_ *Options, planPath string, _, _, _ *os.File, _ []*os.File) (func() int, error) {
		spawned = true
		removeKeeperPlan(planPath)
		return func() int { return 3 }, nil
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.Args = []string{"claude"}
	o.UseProfiles = map[string]string{"claude": "zai"}
	o.WithCredentials = []string{"cerebras"}
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true, RC: 0} }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	rc := Run(*o)
	out := stdout.String() + stderr.String()
	if rc == 0 || spawned {
		t.Fatalf("the launch must refuse claude's zai profile with no zai key before any keeper: rc %d, spawned %v\n%s",
			rc, spawned, out)
	}
	// The grant's disclosure is printed right after the file is staged: without it this cell would
	// pass for a launch refused before staging anything.
	if !strings.Contains(out, "Credential grant (--with-credentials cerebras): this jail holds") ||
		!strings.Contains(out, "ZAI_API_KEY") {
		t.Fatalf("the launch did not reach the staging and then the credential pre-flight:\n%s", out)
	}
	if _, err := os.Stat(filepath.Dir(jailGrantHostFile(cname))); !os.IsNotExist(err) {
		t.Errorf("a launch refused before its container started left its grant file (%v)", err)
	}
}
