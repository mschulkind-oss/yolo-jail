package run

import (
	"bytes"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
)

// THE SEAL THROUGH THE KEEPER: since the keeper starts the host services (keeper.go), the seal
// (seal.go) has to reach the plan the launch hands it, or a sealed fork build's keeper would start
// the loopholes and the credential view the seal forbids. keeperPlanFor carries it, and the plan
// of a sealed launch names no service.
func TestASealedLaunchsKeeperPlanIsSealedAndNamesNoService(t *testing.T) {
	for _, sealed := range []bool{true, false} {
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, t.TempDir(), "podman", &stdout, &stderr, nil)
		o.Sealed = sealed
		plan, err := o.keeperPlanFor(jsonx.NewOrderedMap(), "podman", "yolo-seal", stagedPacks{}, nil, nil, nil, "", "", []string{"podman", "run"}, &assembleInput{})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Sealed != sealed {
			t.Errorf("Sealed=%v: the keeper plan says sealed=%v", sealed, plan.Sealed)
		}
	}
}

// relayWithin is the fixture's relay, bounded: a keeper test fails rather than hangs.
func (f *keeperFixture) relayWithin(d time.Duration) bool {
	f.t.Helper()
	got := make(chan bool, 1)
	go func() { got <- f.relay() }()
	select {
	case ready := <-got:
		return ready
	case <-time.After(d):
		f.t.Fatalf("the keeper's relay neither reached ready nor ended within %s:\n%s", d, f.errOut.String())
		return false
	}
}

// sealFixtureBroker makes the claude pack's broker loophole active for a keeper fixture's plan and
// proves it is: an UNSEALED launch of that config plans the broker's host service and delivers the
// credential view, so a sealed keeper's starting neither is the seal's doing.
func sealFixtureBroker(t *testing.T, p *keeperPlan) {
	t.Helper()
	brokerFixtureDirs(t, true)
	cfg, err := p.config()
	if err != nil {
		t.Fatal(err)
	}
	unsealed := goldenOptions(p.Workspace, t.TempDir())
	unsealed.Getenv = viewEnv("1")
	if names := unsealed.plannedLoopholeNames(p.Runtime, cfg); !slices.Contains(names, broker.BrokerLoopholeName) {
		t.Fatalf("the unsealed fixture plans %v, not the broker, so the seal's gate is unexercised", names)
	}
	if !unsealed.claudeCredentialView(p.Runtime, cfg) {
		t.Fatal("the unsealed fixture delivers no credential view, so the seal's gate is unexercised")
	}
}

// withViewSwitch is the keeper's Getenv with the credential view turned on (claudeview.SwitchEnv).
func withViewSwitch(o *Options) {
	o.Getenv = func(k string) string {
		if k == claudeview.SwitchEnv {
			return "1"
		}
		return os.Getenv(k)
	}
}

// TestASealedKeeperStartsNoServiceAndRegistersNoView is FP-D15 run: a sealed fork build's launch
// spawns its keeper as every fresh container launch does, and that keeper, handed a sealed plan
// naming no service under a config whose unsealed launch would start the claude broker and register
// the credential view, accepts the plan, starts the container, starts no host service (their dir is
// never made), registers no view, and ends the jail with its last session.
func TestASealedKeeperStartsNoServiceAndRegistersNoView(t *testing.T) {
	var mu sync.Mutex
	registered := 0
	saved := registerView
	t.Cleanup(func() { registerView = saved })
	registerView = func(claudeview.Location, string, string) (oauthbroker.RegisterResult, error) {
		mu.Lock()
		registered++
		mu.Unlock()
		return oauthbroker.RegisterResult{Wrote: true}, nil
	}
	var session *sessionLock
	f := startKeeperFixtureWith(t, true, func(p *keeperPlan) {
		sealFixtureBroker(t, p)
		p.Sealed = true
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
	}, withViewSwitch)
	if !f.relayWithin(30 * time.Second) {
		t.Fatalf("the keeper refused a sealed plan, or never reached ready:\n%s", f.errOut.String())
	}
	mu.Lock()
	n := registered
	mu.Unlock()
	if n != 0 {
		t.Errorf("a sealed keeper registered the credential view %d times", n)
	}
	if _, err := os.Stat(f.plan.SocketsDir); !os.IsNotExist(err) {
		t.Errorf("a sealed keeper made the host-services dir (err %v)", err)
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the sealed keeper ended %d", rc)
	}
	if f.jail.stopCount() != 1 {
		t.Errorf("the sealed keeper stopped its jail %d times, want once", f.jail.stopCount())
	}
}

// TestAKeeperRefusesASealedPlanThatCrossesAnything is FP-D15's other half, JL-D68's rule at the
// seal: a sealed plan naming a host service is one this keeper would not start, and one forwarding a
// host port hands the jail a host service's reach (FP-D11), so each is refused before anything
// starts rather than run, or silently cut down to what the seal allows.
func TestAKeeperRefusesASealedPlanThatCrossesAnything(t *testing.T) {
	for _, tc := range []struct {
		name string
		tune func(*keeperPlan)
		want string
	}{
		{"a service", func(p *keeperPlan) { p.Services = []string{broker.BrokerLoopholeName} },
			`the launch disclosed the host service "` + broker.BrokerLoopholeName + `", which this keeper would not start`},
		{"a forward", func(p *keeperPlan) {
			p.Forwards = []PortForward{{LocalPort: 5432, HostPort: 5432}}
			p.ForwardDir = t.TempDir()
		}, "it is sealed, and forwards 1 host port into the jail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := startKeeperFixture(t, true, func(p *keeperPlan) {
				sealFixtureBroker(t, p)
				p.Sealed = true
				tc.tune(p)
			})
			if f.relayWithin(30 * time.Second) {
				t.Fatal("a sealed plan that crosses reached ready")
			}
			if rc := f.wait(); rc != 1 {
				t.Errorf("the keeper exited %d, want 1", rc)
			}
			if got := f.errOut.String(); !strings.Contains(got, "Refusing this launch's plan") || !strings.Contains(got, tc.want) {
				t.Errorf("the refusal did not say why:\n%s", got)
			}
			if _, err := os.Stat(f.plan.SocketsDir); !os.IsNotExist(err) {
				t.Errorf("a refused sealed plan made the host-services dir (err %v)", err)
			}
			if f.jail.stopCount() != 0 || strings.Contains(strings.Join(f.jail.calls, "\n"), "podman run") {
				t.Error("a refused sealed plan started something")
			}
		})
	}
}

// WW-D28: the gate's approved scope, each repository with its source labels, reaches the keeper
// through the plan file: on the container arm the keeper's Options write the scope files and the
// launch line, and nothing in the keeper derives them again (WW-P2).
func TestTheKeepersPlanCarriesTheApprovedScopes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "podman", &stdout, &stderr, nil)
	want := map[string][]config.ScopeRepo{"gb": {{Repo: "o/r", Sources: []string{`remote "origin"`}},
		{Repo: "org/lib", Sources: []string{"yolo-jail.jsonc", `x\x1b[2K.jsonc`}}}}
	o.approvedScopes = want
	plan, err := o.keeperPlanFor(jsonx.NewOrderedMap(), "podman", "yolo-scopes", stagedPacks{}, nil, nil, nil, "", "",
		[]string{"podman", "run"}, &assembleInput{})
	if err != nil {
		t.Fatal(err)
	}
	path, err := writeKeeperPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	read, err := readKeeperPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if k := newKeeper(read, KeeperSeams{}, nil, nil, nil, nil); !reflect.DeepEqual(k.o.approvedScopes, want) {
		t.Fatalf("the keeper holds %+v, want the gate's %+v", k.o.approvedScopes, want)
	}
}
