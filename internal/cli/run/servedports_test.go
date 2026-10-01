package run

// servedports_test.go pins the launcher half of SERVED ADDRESSES (servedaddresses.go;
// docs/plans/notch-convergence.md NC-D41 to NC-D44): on a jail that shares this process's
// network namespace every declared jail-daemon and pack-service address moves to a port picked
// for the launch, and the daemon's argv, its clients' pack env pointer, the provider table and
// the channel's record all name that one port; on a private namespace nothing moves; and an
// attach composes for the running jail's recorded ports and never picks its own.
//
// Everything below runs the production compositions (jailDaemonsFor, composePackChannel through
// assembleInput.envChannel, assembleRunCmd, writeUserEnvFile, rekeyChannelForAttach), so
// deleting any one call site the chain passes through fails a test here.

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// servedPortPacks is a launch running all three kinds of served address: the OpenAI adapter
// (a loophole jail daemon with `listen`), codex's refresh pointer at it, and the wire bridge's
// adapter addresses and via address, with claude on cerebras routed at the first of them.
func servedPortPacks(t *testing.T) []*packload.Pack {
	return []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "cerebras"), officialPack(t, "wire-bridge"),
		officialPack(t, "codex"), officialPack(t, "openai-auth"),
	}
}

// servedPortLaunch assembles servedPortPacks' launch with tune applied, the loophole modules
// recorded the way staging records them.
func servedPortLaunch(t *testing.T, tune func(*Options)) assembled {
	t.Helper()
	packs := servedPortPacks(t)
	cfg := bareConfig()
	configSelects(cfg, "claude", "cerebras")
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	la := zaiLaunchAssembled(t, packs, cfg, cerebrasKey(), func(o *Options) {
		loopholes.SetPackModules(packLoopholeModules(packs))
		if tune != nil {
			tune(o)
		}
	})
	t.Cleanup(la.o.releaseReservedPorts) // Run's deferred release, which this harness does not reach
	return la
}

// adapterListen is the address the OpenAI adapter's argv in the jail-daemon payload binds.
func adapterListen(t *testing.T, argv []string) string {
	t.Helper()
	v := envArgValues(argv, "YOLO_JAIL_DAEMONS")
	if len(v) != 1 {
		t.Fatalf("YOLO_JAIL_DAEMONS on the argv = %q, want one", v)
	}
	var specs []struct {
		Name string   `json:"name"`
		Cmd  []string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(v[0], "YOLO_JAIL_DAEMONS=")), &specs); err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		if s.Name != "openai-auth-broker" {
			continue
		}
		for i, a := range s.Cmd {
			if a == "--listen" && i+1 < len(s.Cmd) {
				return s.Cmd[i+1]
			}
		}
		t.Fatalf("the adapter's argv names no --listen: %v", s.Cmd)
	}
	t.Fatalf("the payload runs no OpenAI adapter: %s", v[0])
	return ""
}

func oneValue(t *testing.T, la assembled, key string) string {
	t.Helper()
	v := la.channelEnv(t, key)
	if len(v) != 1 {
		t.Fatalf("the channel carries %d %s lines, want 1: %q", len(v), key, v)
	}
	return strings.TrimPrefix(v[0], key+"=")
}

func TestASharedNamespaceLaunchMovesEveryServedAddressTogether(t *testing.T) {
	for name, tune := range map[string]func(*Options){
		// network.mode "host": the jail is on this process's loopback.
		"network.mode host": func(o *Options) { o.Network = "host" },
		// A nested podman is forced onto --net=host, whatever the config says.
		"nested podman": func(o *Options) {
			o.PathExists = func(p string) bool { return p == "/run/.containerenv" }
		},
	} {
		t.Run(name, func(t *testing.T) {
			la := servedPortLaunch(t, tune)
			listen := adapterListen(t, la.argv)
			if listen == "127.0.0.1:1460" || !strings.HasPrefix(listen, "127.0.0.1:") {
				t.Fatalf("the adapter still binds %q on a shared namespace; it must move to a picked port", listen)
			}
			if got := oneValue(t, la, "CODEX_REFRESH_TOKEN_URL_OVERRIDE"); got != "http://"+listen+"/oauth/token" {
				t.Errorf("codex's refresh pointer = %q, want the adapter's served address %q", got, listen)
			}
			base := oneValue(t, la, "ANTHROPIC_BASE_URL")
			if base == "http://127.0.0.1:8214" || !strings.HasPrefix(base, "http://127.0.0.1:") {
				t.Errorf("claude is routed at %q; the bridge's adapter address must move too", base)
			}
			providers := oneValue(t, la, "YOLO_PROVIDERS")
			if !strings.Contains(providers, `"base_url": "`+base+`"`) {
				t.Errorf("the provider table the bridge binds from does not name claude's %s:\n%s", base, providers)
			}

			moved := parseServedAddresses(oneValue(t, la, paths.ServedAddressesEnv))
			for _, declared := range []string{"127.0.0.1:1460", "127.0.0.1:8214", "127.0.0.1:8215", "127.0.0.1:8216"} {
				if moved[declared] == "" {
					t.Errorf("the channel records no served address for %s: %v", declared, moved)
				}
			}
			if moved["127.0.0.1:1460"] != listen || "http://"+moved["127.0.0.1:8214"] != base {
				t.Errorf("the recorded map %v disagrees with the argv (%s) or the table (%s)", moved, listen, base)
			}
			seen := map[string]bool{}
			for _, to := range moved {
				if seen[to] {
					t.Errorf("two declared addresses share the served address %s: %v", to, moved)
				}
				seen[to] = true
			}
		})
	}
}

// A PRIVATE NAMESPACE MOVES NOTHING (NC-D42): the bridged default keeps every declared port,
// on the argv, in the pointer and in the table, and records no map.
func TestAPrivateNamespaceLaunchKeepsEveryDeclaredAddress(t *testing.T) {
	la := servedPortLaunch(t, nil)
	if got := adapterListen(t, la.argv); got != "127.0.0.1:1460" {
		t.Errorf("the adapter binds %q on a bridged jail, want its declared 127.0.0.1:1460", got)
	}
	if got := oneValue(t, la, "CODEX_REFRESH_TOKEN_URL_OVERRIDE"); got != "http://127.0.0.1:1460/oauth/token" {
		t.Errorf("codex's refresh pointer = %q on a bridged jail", got)
	}
	if got := oneValue(t, la, "ANTHROPIC_BASE_URL"); got != "http://127.0.0.1:8214" {
		t.Errorf("claude is routed at %q on a bridged jail", got)
	}
	if v := la.channelEnv(t, paths.ServedAddressesEnv); len(v) != 0 {
		t.Errorf("a bridged launch records served addresses: %q", v)
	}
}

// ONE PROCESS SETTLES ONCE: a second composition in the same launch (Run composes the channel
// and the payload separately; an attach composes again) names the same ports.
func TestOneLaunchComposesOneSetOfServedAddresses(t *testing.T) {
	la := servedPortLaunch(t, func(o *Options) { o.Network = "host" })
	first := la.o.movedServedAddresses()
	again := la.o.jailDaemonsFor(la.in.cfg, "podman", la.in.packs)
	if got := la.o.movedServedAddresses(); len(got) != len(first) || got["127.0.0.1:1460"] != first["127.0.0.1:1460"] {
		t.Errorf("a second composition moved the ports: %v then %v", first, got)
	}
	for _, s := range again {
		if s.Name == "openai-auth-broker" && s.Listen != first["127.0.0.1:1460"] {
			t.Errorf("the second payload's adapter listens at %s, want %s", s.Listen, first["127.0.0.1:1460"])
		}
	}
}

// EVERY PICK IS HELD UNTIL ITS SERVER HAS IT (NC-D69): a shared-namespace launch's picked ports
// are distinct and none is the declared one; while the launch holds them nothing else can bind
// one; and once it releases them (what it does just before the process that starts the jail's
// daemons) each is free for its daemon to bind. Through jailDaemonsFor's settle, the production
// pick.
func TestASharedNamespaceLaunchHoldsEveryPortItPickedUntilItReleasesThem(t *testing.T) {
	la := servedPortLaunch(t, func(o *Options) { o.Network = "host" })
	moved := la.o.movedServedAddresses()
	if len(moved) == 0 {
		t.Fatal("fixture: the shared-namespace launch picked nothing")
	}
	seen := map[string]bool{}
	for from, to := range moved {
		if seen[to] || to == from {
			t.Errorf("pick %s -> %s repeats or keeps the declared port: %v", from, to, moved)
		}
		seen[to] = true
		if l, err := net.Listen("tcp", to); err == nil {
			_ = l.Close()
			t.Errorf("another listener bound %s while this launch held it for %s's daemon", to, from)
		}
	}
	la.o.releaseReservedPorts()
	for from, to := range moved {
		l, err := net.Listen("tcp", to)
		if err != nil {
			t.Errorf("%s, picked for %s, is not free for its daemon once released: %v", to, from, err)
			continue
		}
		_ = l.Close()
	}
}

// writeRunningChannel writes, as the running jail's own launch would have, a channel file whose
// section records moved.
func writeRunningChannel(t *testing.T, wsState string, moved map[string]string) {
	t.Helper()
	body := "# header\n" + channelSectionHeader + "\n" +
		exportPlain("YOLO_PROVIDERS", "{}") + exportPlain("YOLO_PROFILES", "{}") +
		exportPlain("YOLO_USE_PROFILES", "{}")
	if v := servedAddressesValue(moved); v != "" {
		body += exportPlain(paths.ServedAddressesEnv, v)
	}
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsState, "yolo-user-env.sh"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// THE ATTACH RE-READS THE RUNNING JAIL'S PORTS, the way it re-reads its caller tokens: the
// jail's daemons bound them at boot, so a channel composed for this process's own picks is
// composed again for the recorded ones, and one composed for a jail that recorded none serves
// the declared addresses even on a shared namespace. Through rekeyChannelForAttach, the call
// attachExisting makes; deleting its adoption fails both halves.
func TestAnAttachComposesForTheRunningJailsServedAddresses(t *testing.T) {
	packs := servedPortPacks(t)
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	tune := func(o *Options, cfg *jsonx.OrderedMap) {
		loopholes.SetPackModules(packLoopholeModules(packs))
		o.Network = "host"
		configSelects(cfg, "claude", "cerebras")
	}
	compose := func(o *Options, cfg *jsonx.OrderedMap) func([]*packload.Pack) (*packChannel, error) {
		return func(p []*packload.Pack) (*packChannel, error) { return o.composePackChannel(cfg, p, cerebrasKey()) }
	}

	o, cfg, channel, _ := attachFixture(t, currentJailEnv, packs, cerebrasKey(), tune)
	picked := channel.servedAddresses["127.0.0.1:8214"]
	if picked == "" {
		t.Fatalf("fixture: the shared-namespace composition moved nothing: %v", channel.servedAddresses)
	}
	wsState := paths.WorkspaceHomeState(o.Workspace)
	running := map[string]string{
		"127.0.0.1:1460": "127.0.0.1:41460", "127.0.0.1:8214": "127.0.0.1:48214",
		"127.0.0.1:8215": "127.0.0.1:48215", "127.0.0.1:8216": "127.0.0.1:48216",
	}
	writeRunningChannel(t, wsState, running)
	rekeyed, err := o.rekeyChannelForAttach(wsState, packs, channel, compose(o, cfg))
	if err != nil {
		t.Fatal(err)
	}
	if !rekeyed.servedAddressesAgree(running) {
		t.Errorf("the attach composed for %v, want the running jail's %v", rekeyed.servedAddresses, running)
	}
	shared, agents := deliveredFiles(t, rekeyed)
	if !strings.Contains(agents["claude"], "export ANTHROPIC_BASE_URL=${ANTHROPIC_BASE_URL:-'http://127.0.0.1:48214'}\n") {
		t.Errorf("claude after the attach is not routed at the running bridge's port:\n%s", agents["claude"])
	}
	if !strings.Contains(shared, "CODEX_REFRESH_TOKEN_URL_OVERRIDE='http://127.0.0.1:41460/oauth/token'") {
		t.Errorf("codex after the attach does not point at the running adapter's port:\n%s", shared)
	}
	if strings.Contains(shared, picked) {
		t.Errorf("the attach delivered this process's own pick %s, which no daemon in the jail binds", picked)
	}

	// A jail that recorded nothing (bridged, or launched before served addresses) serves the
	// declared ports; the attach never picks its own, even on a shared namespace.
	o2, cfg2, channel2, _ := attachFixture(t, currentJailEnv, packs, cerebrasKey(), tune)
	wsState2 := paths.WorkspaceHomeState(o2.Workspace)
	writeRunningChannel(t, wsState2, nil)
	rekeyed2, err := o2.rekeyChannelForAttach(wsState2, packs, channel2, compose(o2, cfg2))
	if err != nil {
		t.Fatal(err)
	}
	if len(rekeyed2.servedAddresses) != 0 {
		t.Errorf("an attach to a jail that recorded no served addresses picked %v", rekeyed2.servedAddresses)
	}
	shared2, agents2 := deliveredFiles(t, rekeyed2)
	if !strings.Contains(agents2["claude"], "export ANTHROPIC_BASE_URL=${ANTHROPIC_BASE_URL:-'http://127.0.0.1:8214'}\n") {
		t.Errorf("claude after the attach is not routed at the declared bridge port:\n%s", agents2["claude"])
	}
	if strings.Contains(shared2, paths.ServedAddressesEnv) {
		t.Errorf("the attach recorded served addresses the running jail never bound:\n%s", shared2)
	}
}

// The running jail's map is read from a file the jail can write, so only loopback pairs survive.
func TestParseServedAddressesKeepsOnlyLoopbackPairs(t *testing.T) {
	got := parseServedAddresses(`{"127.0.0.1:1460":"127.0.0.1:40000","127.0.0.1:1461":"10.0.0.1:40001",` +
		`"example.com:80":"127.0.0.1:40002","[::1]:8214":"[::1]:40003"}`)
	if len(got) != 2 || got["127.0.0.1:1460"] != "127.0.0.1:40000" || got["[::1]:8214"] != "[::1]:40003" {
		t.Errorf("parseServedAddresses = %v", got)
	}
	if parseServedAddresses("not json") != nil || parseServedAddresses("") != nil {
		t.Error("a malformed or empty line yielded a map")
	}
}
