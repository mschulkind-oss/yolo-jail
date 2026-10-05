package run

// jailgrant_test.go pins --with-credentials AT A JAIL LAUNCH (jailgrant.go;
// docs/design/credential-sources-separation.md OQ-ES5's jail half, ES-D31 to ES-D36), one cell per
// vehicle and per rule, each driven through the production path that carries it: Run for the
// resolution and the macos-user arm, a whole podman launch to its keeper's plan, the assembler on
// Apple Container, the keeper to its main process's client, and attachExisting for the attach
// rule. The front door's parse is internal/cli's jailwithcredentials_test.go.

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

// PODMAN, through a whole fresh launch to its keeper's plan: the container argv names each granted
// key as a bare `-e NAME` and carries no value anywhere; the values ride the plan for the runtime
// client alone; the plan's grant, which the keeper writes into its start record, holds names only;
// and nothing the launch wrote into the workspace holds a granted value.
func TestAPodmanJailGrantNamesKeysOnTheArgvAndHandsValuesToTheClient(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, grantConfig(t, `"zai", "cerebras"`, ""))
	ws := t.TempDir()
	var plan *keeperPlan
	saved := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = saved })
	defaultKeeperSpawner = func(_ *Options, planPath string, _, _, _ *os.File, _ []*os.File) (func() int, error) {
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
	cname := yoloruntime.FromWorkspace(ws)
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	_ = Run(*o)
	out := stdout.String() + stderr.String()
	if plan == nil {
		t.Fatalf("the launch never reached its keeper's spawn:\n%s", out)
	}
	if i := slices.Index(plan.RunCmd, "ZAI_API_KEY"); i < 1 || plan.RunCmd[i-1] != "-e" {
		t.Errorf("the container argv must name the granted key as a bare `-e ZAI_API_KEY`: %q", plan.RunCmd)
	}
	for _, w := range plan.RunCmd {
		for _, v := range grantValues {
			if strings.Contains(w, v) {
				t.Errorf("the container argv carries a credential value in %q", w)
			}
		}
		if strings.HasPrefix(w, "CEREBRAS_API_KEY") {
			t.Errorf("cerebras was not named, yet the argv carries %q", w)
		}
	}
	if !slices.Equal(plan.GrantEnv, []string{"ZAI_API_KEY=tok-z"}) {
		t.Errorf("the plan hands the runtime client %q, want exactly ZAI_API_KEY=tok-z", plan.GrantEnv)
	}
	if plan.Grant == nil || !slices.Equal(plan.Grant.Providers, []string{"zai"}) {
		t.Fatalf("the plan's grant (for the keeper's start record) = %+v, want the zai grant", plan.Grant)
	}
	rec, _ := json.Marshal(keeperRecord{PID: 1, Grant: plan.Grant})
	if !strings.Contains(string(rec), `"ZAI_API_KEY"`) || strings.Contains(string(rec), "tok-z") {
		t.Errorf("the start record's grant must hold the name and never the value: %s", rec)
	}
	if hits := filesHolding(t, ws, "tok-z"); len(hits) != 0 {
		t.Errorf("the granted value reached a file in the workspace: %v", hits)
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

// APPLE CONTAINER shares the argv: the same bare `-e NAME`, before every `-e` yolo writes itself,
// and no value.
func TestAnAppleContainerJailGrantNamesKeysOnTheArgv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	packs := []*packload.Pack{officialPack(t, "zai"), officialPack(t, "cerebras")}
	in := relocationInput(t, "container", t.TempDir(), nil)
	in.packs = packs
	store := jsonx.NewOrderedMap()
	store.Set("ZAI_API_KEY", "tok-z")
	store.Set("CEREBRAS_API_KEY", "tok-c")
	in.channel = channelFor(t, o, in.cfg, packs, store)
	o.WithCredentials = []string{"cerebras"}
	if err := o.resolveJailGrant(in.channel); err != nil {
		t.Fatal(err)
	}
	argv := o.assembleRunCmd(in)
	i := slices.Index(argv, "CEREBRAS_API_KEY")
	if i < 1 || argv[i-1] != "-e" {
		t.Fatalf("Apple Container's argv must name the granted key as a bare `-e CEREBRAS_API_KEY`: %q", argv)
	}
	if first := slices.Index(argv, "-e"); first != i-1 {
		t.Errorf("the grant's `-e` must come before every `-e` yolo writes (first -e at %d, the grant's at %d)", first, i-1)
	}
	for _, w := range argv {
		if strings.Contains(w, "tok-") || strings.HasPrefix(w, "ZAI_API_KEY") {
			t.Errorf("Apple Container's argv carries %q", w)
		}
	}
}

// THE KEEPER hands the grant's values to the container's runtime client, and to nothing else of
// its own, and records the names, never the values, in its start record for the attach to read.
func TestTheKeeperHandsTheGrantToTheContainerClientAndRecordsNames(t *testing.T) {
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
		p.Grant, p.GrantEnv = grant, []string{"ZAI_API_KEY=tok-z"}
		p.RunCmd = []string{"sh", "-c", `printf '%s' "$ZAI_API_KEY" > ` + shquote.Quote(seen) + "; " + p.RunCmd[2]}
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	if b, err := os.ReadFile(seen); err != nil || string(b) != "tok-z" {
		t.Errorf("the container's client was handed ZAI_API_KEY = %q (err %v), want tok-z", b, err)
	}
	if v := os.Getenv("ZAI_API_KEY"); v == "tok-z" {
		t.Error("the keeper put the granted value in its own environment, which every host service inherits")
	}
	rec, ok := readKeeperRecord(f.cname)
	if !ok || rec.Grant == nil || !rec.Grant.holds("ZAI_API_KEY") || rec.Grant.Spelled != "zai" {
		t.Errorf("the start record's grant = %+v (%v), want the zai grant by name", rec.Grant, ok)
	}
	raw, _ := os.ReadFile(keeperRecordPath(f.cname))
	if strings.Contains(string(raw), "tok-z") {
		t.Errorf("the start record holds a credential value:\n%s", raw)
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
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
				"'yolo stop' from this workspace", "`yolo --with-credentials zai,cerebras -- claude` (the jail's grant"},
			[]string{"Attaching to"}},
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
