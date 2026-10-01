package launchservice

// listener_test.go pins a credential DOORWAY's two halves in this package
// (docs/design/host-notch-services.md HS-D15): its admission (AdmitDoorway, the rule every host
// half passes), its plan at the launch's own address and token (PlanAt), and its daemon side
// (ServeListener), run as this test binary under Start exactly as a launch runs
// `yolo internal daemon <adapter>`.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fakeDoorway is a doorway in miniature, on ServeListener: its listen address is its argv's last
// word, it answers a request carrying its caller token with the input's UPSTREAM value, and one
// without it with 401. mode "fail" refuses in its prepare, before anything is bound. It serves
// the service doorwayServiceEnv names, "door" when that is unset.
func fakeDoorway(mode string) int {
	listen := os.Args[len(os.Args)-1]
	service := os.Getenv(doorwayServiceEnv)
	if service == "" {
		service = "door"
	}
	return ServeListener(service, listen, func(getenv func(string) string) (func(net.Listener) error, error) {
		if mode == "fail" {
			return nil, errors.New("the input lacks its upstream\nsecond line")
		}
		token, upstream := getenv(paths.ServiceCallerTokenEnv(service)), getenv("UPSTREAM")
		return func(l net.Listener) error {
			return http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != token {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				fmt.Fprint(w, upstream)
			}))
		}, nil
	})
}

// doorwayPlan is a doorway's plan at a port reserved as a launch reserves its doorways' (reserve.go).
func doorwayPlan(t *testing.T) (*Plan, string) {
	t.Helper()
	held, err := ReservePorts([]string{"127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	r := held["127.0.0.1:1"]
	t.Cleanup(r.Release)
	addr := r.Addr()
	return PlanAt(Declared{Service: "door", Pack: "p", Cmd: []string{"yolo", "doorway", addr}},
		addr, paths.ServiceCallerTokenEnv("door"), strings.Repeat("cd", 32), r), addr
}

func get(t *testing.T, addr, auth string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", addr, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// A DOORWAY STARTS, SERVES ITS INPUT BEHIND ITS TOKEN, AND STOPS: Start returns once ServeListener
// has bound the plan's address and answered `ready`, the input file's variables reach its serve,
// only the plan's token gets past it, and Stop leaves nothing listening.
func TestADoorwayServesBehindItsPlansTokenUntilStopped(t *testing.T) {
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "doorway:ok")
	plan, addr := doorwayPlan(t)
	if got := plan.Addresses(); len(got) != 1 || got[0] != addr {
		t.Fatalf("PlanAt's addresses = %v, want the one address it was handed", got)
	}
	r, err := Start(plan, map[string]string{"UPSTREAM": "from-the-launch"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, addr, ""); code != http.StatusUnauthorized {
		t.Errorf("a request without the token got %d, want 401", code)
	}
	if code, body := get(t, addr, plan.Token); code != http.StatusOK || body != "from-the-launch" {
		t.Errorf("a request with the token got %d %q, want 200 and the input's value", code, body)
	}
	r.Stop()
	waitGone(t, r, 3*time.Second)
	if dials(addr) {
		t.Error("the doorway still listens after Stop")
	}
}

// A DOORWAY ENDS WITH ITS LAUNCH, with no signal sent: closing the lifeline, which is what the
// kernel does when the launch is SIGKILLed or crashes before its deferred Stop, ends
// ServeListener and frees the address. TestAServiceEndsWhenItsLaunchIsGone pins the same bound
// for fakeHostHalf's body, which reads the lifeline itself; this pins ServeListener's own read of
// it, the one every doorway runs, so a doorway a killed launch left behind cannot keep answering
// on the host's loopback. Deleting the Lifeline call from serveListener fails this.
func TestADoorwayEndsWhenItsLaunchIsGone(t *testing.T) {
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "doorway:ok")
	plan, addr := doorwayPlan(t)
	r, err := Start(plan, map[string]string{"UPSTREAM": "from-the-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	if !dials(addr) {
		t.Fatal("Start returned before the doorway was listening")
	}
	_ = r.lifeline.Close() // what the kernel does when the launch process dies
	waitGone(t, r, 5*time.Second)
	if dials(addr) {
		t.Error("the doorway still listens after its launch's lifeline closed")
	}
}

// A DOORWAY THAT CANNOT PREPARE REFUSES THE START, with its reason on one line, before it binds.
func TestADoorwayThatCannotPrepareRefusesTheStart(t *testing.T) {
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "doorway:fail")
	plan, addr := doorwayPlan(t)
	_, err := Start(plan, nil)
	if err == nil || !strings.Contains(err.Error(), "the input lacks its upstream second line") {
		t.Fatalf("Start = %v, want the doorway's own reason, flattened", err)
	}
	if dials(addr) {
		t.Error("a doorway that refused still bound its address")
	}
}

// ADMISSION OVER A PAYLOAD (AdmitDoorways, what the launch and `yolo check` both apply): an
// official pack's doorway keeps its host argv; a local pack's loses it and is reported, naming
// its pack; a pack service's daemon and an intercepting loophole's are neither admitted nor
// refused, since no launch opens either outside; and the caller's slice is not written.
func TestAdmitDoorwaysClearsAndReportsOnlyTheRefusedDoorways(t *testing.T) {
	loophole := func(n string) string {
		return `{"kind": "loophole", "from": "loopholes/` + n + `"}`
	}
	official := packFrom(t, "creds", `{"name": "creds", "contributes": [`+loophole("creds-door")+`]}`, true)
	local := packFrom(t, "local", `{"name": "local", "contributes": [`+loophole("acme-door")+`, `+
		loophole("acme-intercept")+`]}`, false)
	host := []string{"yolo", "internal", "daemon", "x", "--listen", "{listen}"}
	specs := []loopholes.JailDaemonSpec{
		{Name: "creds-door", HostCmd: host},
		{Name: "acme-door", HostCmd: host},
		{Name: "acme-intercept", HostCmd: host, Intercepts: true},
		{Name: "wire-bridge", HostCmd: host, Service: true},
		{Name: "plain"},
	}
	got, refused := AdmitDoorways([]*packload.Pack{official, local}, specs)
	if len(refused) != 1 || refused[0].Name != "acme-door" || refused[0].Pack != "local" ||
		!strings.Contains(refused[0].Why, "its pack is not one yolo ships") {
		t.Fatalf("refused = %+v, want the local pack's acme-door alone, with the admission reason", refused)
	}
	kept := map[string]bool{}
	for _, s := range got {
		kept[s.Name] = len(s.HostCmd) > 0
	}
	want := map[string]bool{"creds-door": true, "acme-door": false, "acme-intercept": true,
		"wire-bridge": true, "plain": false}
	for name, w := range want {
		if kept[name] != w {
			t.Errorf("%s keeps its host argv = %v, want %v", name, kept[name], w)
		}
	}
	if len(specs[1].HostCmd) == 0 {
		t.Error("AdmitDoorways cleared the host argv in the caller's slice")
	}
}

// A DOORWAY IS ADMITTED BY THE SAME RULE AS A HOST HALF: a pack yolo ships, an argv naming `yolo`.
// A fetched or local pack's is refused by name, and so is a loophole no selected pack ships.
func TestAdmitDoorwayAppliesTheHostHalfRule(t *testing.T) {
	const manifest = `{"name": "creds", "contributes": [{"kind": "loophole", "from": "loopholes/creds"}]}`
	official := packFrom(t, "creds", manifest, true)
	fetched := packFrom(t, "creds", manifest, false)
	cmd := []string{"yolo", "internal", "daemon", "creds-adapter", "--listen", "127.0.0.1:1"}
	d, err := AdmitDoorway([]*packload.Pack{official}, "creds", "creds", cmd)
	if err != nil || d.Service != "creds" || d.Pack != "creds" || strings.Join(d.Cmd, " ") != strings.Join(cmd, " ") {
		t.Fatalf("AdmitDoorway(official) = %+v, %v", d, err)
	}
	for name, tc := range map[string]struct {
		packs []*packload.Pack
		pack  string
		cmd   []string
		want  string
	}{
		"a fetched pack":  {[]*packload.Pack{fetched}, "creds", cmd, "its pack is not one yolo ships"},
		"a later fetched": {[]*packload.Pack{official, fetched}, "creds", cmd, "its pack is not one yolo ships"},
		"not yolo":        {[]*packload.Pack{official}, "creds", []string{"python3", "x"}, "must name `yolo`"},
		"no pack":         {[]*packload.Pack{official}, "", cmd, "no selected pack ships the loophole"},
		"no argv":         {[]*packload.Pack{official}, "creds", nil, "declares no host argv"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := AdmitDoorway(tc.packs, tc.pack, "creds", tc.cmd)
			var adm *AdmissionError
			if !errors.As(err, &adm) || !strings.Contains(adm.Why, tc.want) {
				t.Errorf("AdmitDoorway = %v, want an AdmissionError saying %q", err, tc.want)
			}
		})
	}
}
