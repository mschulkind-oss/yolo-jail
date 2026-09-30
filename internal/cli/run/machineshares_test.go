package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// fakePodmanMachine makes o's podman a machine whose config file records shares, written
// under a temp dir: its Exec seam answers the three podman probes the share read makes
// (runtime.ReadMachineShares), counting them in probes. A nil shares list makes every probe
// fail — the unreadable machine.
//
// IT OWNS THE ENVIRONMENT THAT PICKS THE MACHINE TOO. ReadMachineShares answers UNKNOWN,
// asking podman nothing, whenever CONTAINER_HOST or CONTAINER_CONNECTION is set, since
// podman then talks to a connection it cannot map to a machine. Both are common exports on
// a Mac with more than one machine or a remote podman, and o.Getenv defaults to the test
// process's own environment, so on such a developer's machine every case here saw no
// machine at all: a refusal case refused nothing, and an inertness case passed for the
// wrong reason. The two names read as unset; every other name still reaches the real
// environment.
func fakePodmanMachine(t *testing.T, o *Options, shares []string, probes *int) {
	t.Helper()
	getenv := o.Getenv
	o.Getenv = func(k string) string {
		if k == "CONTAINER_HOST" || k == "CONTAINER_CONNECTION" {
			return ""
		}
		return getenv(k)
	}
	o.Exec = machineShareExec(t, shares, probes)
}

// machineShareExec is fakePodmanMachine's Exec seam.
func machineShareExec(t *testing.T, shares []string, probes *int) func([]string, string, []string, time.Duration) ExecResult {
	t.Helper()
	dir := t.TempDir()
	if shares != nil {
		var mounts []string
		for _, s := range shares {
			mounts = append(mounts, `{"Source": "`+s+`", "Target": "`+s+`", "Type": "virtiofs"}`)
		}
		body := `{"Name": "podman-machine-default", "Mounts": [` + strings.Join(mounts, ",") + `]}`
		if err := os.WriteFile(filepath.Join(dir, "podman-machine-default.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		cmd := strings.Join(argv, " ")
		if !strings.HasPrefix(cmd, "podman machine ") && !strings.HasPrefix(cmd, "podman system connection ") {
			return ExecResult{}
		}
		*probes++
		if shares == nil {
			return ExecResult{Ran: true, RC: 125}
		}
		switch {
		case cmd == "podman machine list --format json":
			return ExecResult{Ran: true, Stdout: `[{"Name": "podman-machine-default", "Default": true, "Running": true}]`}
		case cmd == "podman machine inspect podman-machine-default":
			return ExecResult{Ran: true, Stdout: `[{"ConfigDir": {"Path": "` + dir + `"}, "Name": "podman-machine-default"}]`}
		}
		return ExecResult{Ran: true, RC: 125}
	}
}

// defaultMacShares is Podman's own default share list on a Mac (containers.conf's
// getDefaultMachineVolumes for darwin), which does NOT include Homebrew's Cellar.
var defaultMacShares = []string{"/Users", "/private", "/var/folders"}

// sharesMissing is defaultMacShares less every share that covers p, as written or resolved.
// On a Mac t.TempDir() is under /var/folders -> /private/var/folders, both default shares,
// so a fixture meant to sit OUTSIDE the defaults must drop the share that covers it there.
func sharesMissing(t *testing.T, p string) []string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range defaultMacShares {
		if !pathUnder(p, s) && !pathUnder(resolved, s) {
			out = append(out, s)
		}
	}
	return out
}

func pathUnder(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// initCommandPrefix is the recreate command's spelling for shares, up to the added -v.
func initCommandPrefix(shares []string) string {
	cmd := "podman machine init"
	for _, s := range shares {
		cmd += " -v " + s + ":" + s
	}
	return cmd + " -v "
}

// TestResolveJailPrefixConsultsTheMachineShares is the CALL-SITE pin for the prefix half:
// a darwin podman launch whose prebuilt bundle is outside every share the machine
// records — the Homebrew-Cellar case — refuses in resolveJailPrefix, before the image
// load, naming the path and the recreate command. Delete the unsharedBindSources call
// there and the first row fails.
func TestResolveJailPrefixConsultsTheMachineShares(t *testing.T) {
	root := stageBundle(t, true)
	cases := []struct {
		name   string
		rt     string
		shares []string
		refuse bool
		probed bool
	}{
		{"a bundle outside the default shares is refused", "podman", sharesMissing(t, root), true, true},
		// The guide's machine: a share covering the bundle (Cellar, in real life).
		{"a machine that shares the bundle's folder is accepted", "podman",
			append(append([]string{}, defaultMacShares...), filepath.Dir(root)), false, true},
		// TRI-STATE: an unreadable list refuses nothing.
		{"an unreadable share list is silent", "podman", nil, false, true},
		{"apple container is never asked", "container", defaultMacShares, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, _ := prefixOptions(t, func(string) (string, []string) {
				t.Fatal("a prebuilt bundle must not build")
				return "", nil
			})
			o.IsMacOS = true
			probes := 0
			fakePodmanMachine(t, o, tc.shares, &probes)
			var buf bytes.Buffer
			o.Stderr = &buf

			_, ok := o.resolveJailPrefix(root, tc.rt)
			if ok == tc.refuse {
				t.Fatalf("ok=%v, want refuse=%v; stderr:\n%s", ok, tc.refuse, buf.String())
			}
			if (probes > 0) != tc.probed {
				t.Errorf("probed podman %d times, want probed=%v", probes, tc.probed)
			}
			if !tc.refuse {
				return
			}
			for _, want := range []string{
				root, "statfs", "podman machine rm",
				initCommandPrefix(tc.shares),
			} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("refusal lacks %q:\n%s", want, buf.String())
				}
			}
		})
	}
}

// Linux podman has no machine: nothing is asked and nothing refused, whatever the paths.
func TestUnsharedBindSourcesIsInertOffMac(t *testing.T) {
	probes := 0
	o := &Options{IsMacOS: false}
	fillDefaults(o)
	fakePodmanMachine(t, o, defaultMacShares, &probes)
	if msg := o.unsharedBindSources("podman", []string{"/opt/anything"}); msg != "" || probes != 0 {
		t.Errorf("a Linux launch consulted a Podman Machine (probes=%d): %q", probes, msg)
	}
}

// The two pre-flights share one read of the machine.
func TestMachineSharesAreReadOncePerLaunch(t *testing.T) {
	probes := 0
	o := &Options{IsMacOS: true}
	fillDefaults(o)
	fakePodmanMachine(t, o, defaultMacShares, &probes)
	_ = o.unsharedBindSources("podman", []string{"/Users/me/a"})
	first := probes
	_ = o.unsharedBindSources("podman", []string{"/Users/me/b"})
	if first == 0 || probes != first {
		t.Errorf("probes after one check %d, after two %d — the list must be read once", first, probes)
	}
}

// TestRunChecksEveryBindSourceBeforeTheContainerStarts is the CALL-SITE pin for the
// whole-argv half: Run hands the ASSEMBLED argv's bind sources to unsharedBindSources,
// after assembly and before the container is started. An expression pin, in the shape of
// TestRunNormalResolvesAndThreadsTheJailPrefix, because driving Run to that point needs a
// loaded image; TestNoReturnAfterTheSkeletonLeaksIt covers the refusal's return.
func TestRunChecksEveryBindSourceBeforeTheContainerStarts(t *testing.T) {
	src := runSource(t)
	call := strings.Index(src, "o.unsharedBindSources(rt, bindSources(runCmd, in.imageRef))")
	assembled := strings.Index(src, "runCmd := o.assembleRunCmd(in)")
	// The container is started by the keeper this launch spawns (keeperspawn.go).
	started := strings.Index(src, "o.startKeeper(plan)")
	if call < 0 {
		t.Fatal("Run no longer checks the assembled argv's bind sources against the Podman " +
			"Machine's shares: a workspace, `mounts` or `host_files` source the VM cannot see " +
			"would fail as `statfs …` at rc 125 with nothing from yolo first")
	}
	if assembled < 0 || started < 0 || !(assembled < call && call < started) {
		t.Errorf("the bind-source check is not between assembly (%d) and the container start "+
			"(%d): at %d", assembled, started, call)
	}
}

// TestBindSourcesReadsTheAssembledArgv: on a real podman argv, the extraction finds both
// jail-prefix sources and the workspace, and stops at the image ref — what follows it is
// the jailed command, whose arguments are not podman's.
func TestBindSourcesReadsTheAssembledArgv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("agents", []any{"claude"}, "security", sec),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		jailPrefix:   goldenJailPrefix,
		packs:        claudePackFixture(t),
		agentsPath:   filepath.Join(home, "agents"),
		wsState:      filepath.Join(home, "wsstate"),
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	got := bindSources(append(argv, "-v", "/after/the/image:/x"), goldenImageRef)
	have := map[string]bool{}
	for _, s := range got {
		have[s] = true
	}
	for _, want := range []string{goldenJailPrefix.binDir, goldenJailPrefix.shareDir, "/ws"} {
		if !have[want] {
			t.Errorf("bindSources missed %q; got %v", want, got)
		}
	}
	if have["/after/the/image"] {
		t.Error("bindSources read past the image ref into the jailed command")
	}
}

func TestBindSourcesForms(t *testing.T) {
	argv := []string{"podman", "run", "-e", "X=-v", "-v", "/a:/x:ro", "--volume=/b:/y",
		"--mount", "type=bind,source=/c,target=/z", "--mount", "type=tmpfs,target=/t",
		"--volume", "named:/n", "IMG", "-v", "/d:/w"}
	got := strings.Join(bindSources(argv, "IMG"), ",")
	if got != "/a,/b,/c,named" {
		t.Errorf("got %s", got)
	}
}
