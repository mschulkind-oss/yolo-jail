package launchservice

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE ONE SERVICE COMPOSER carries what the macos-user guest's split reads, from the declaration:
// whether the service declares a host half, whether it serves an adaptation, and its endpoint.
// Restart defaults to on-failure, CallerToken is always set, and the specs are sorted by name.
func TestServiceJailDaemonsCarriesTheDeclarationsGuestFacts(t *testing.T) {
	bridge := packFrom(t, "wire-bridge", fmt.Sprintf(bridgeManifest, `["yolo", "internal", "daemon", "wire-bridge"]`), true)
	worker := packFrom(t, "acme", `{"contributes": [
		{"kind": "service", "name": "acme-worker", "jail_daemon": {"cmd": ["acme-worker"], "restart": "always"},
		 "host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-worker"]}}]}`, false)
	plain := packFrom(t, "plain", `{"contributes": [
		{"kind": "service", "name": "acme-svc", "jail_daemon": {"cmd": ["acme-svc"]}}]}`, false)
	specs := ServiceJailDaemons([]*packload.Pack{bridge, worker, plain})
	var names []string
	got := map[string]loopholes.JailDaemonSpec{}
	for _, s := range specs {
		names = append(names, s.Name)
		got[s.Name] = s
	}
	if strings.Join(names, ",") != "acme-svc,acme-worker,wire-bridge" {
		t.Fatalf("composed %v, want the three services sorted by name", names)
	}
	for _, s := range specs {
		if !s.Service || !s.CallerToken {
			t.Errorf("%s: Service %v, CallerToken %v; a service's daemon is both", s.Name, s.Service, s.CallerToken)
		}
	}
	if b := got["wire-bridge"]; !b.HostHalf || !b.ServesAdaptation || b.Endpoint != "wire-bridge.endpoint" || b.Restart != "on-failure" {
		t.Errorf("the bridge's spec is %+v, want a host half, an adaptation, its endpoint and the default restart", b)
	}
	if w := got["acme-worker"]; !w.HostHalf || w.ServesAdaptation || w.Endpoint != "" || w.Restart != "always" {
		t.Errorf("the worker's spec is %+v, want a host half, no adaptation, no endpoint, its own restart", w)
	}
	if p := got["acme-svc"]; p.HostHalf || p.ServesAdaptation || p.Endpoint != "" {
		t.Errorf("the jail-daemon-only service's spec is %+v, want no host half, adaptation or endpoint", p)
	}
}

// AdmitServiceHosts clears the host half of a service the launch will not run, naming it, its
// pack and why, and leaves an admitted one and every other spec as they were. The input is not
// edited: the launch and `yolo check` each hold their own copy.
func TestAdmitServiceHostsClearsARefusedHostHalfByName(t *testing.T) {
	official := packFrom(t, "wire-bridge", fmt.Sprintf(bridgeManifest, `["yolo", "internal", "daemon", "wire-bridge"]`), true)
	local := packFrom(t, "local", `{"contributes": [
		{"kind": "service", "name": "acme-svc", "jail_daemon": {"cmd": ["acme-svc"]},
		 "host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme-svc"]}}]}`, false)
	packs := []*packload.Pack{official, local}
	in := append(ServiceJailDaemons(packs), loopholes.JailDaemonSpec{Name: "a-loophole", Cmd: []string{"x"}})
	out, refused := AdmitServiceHosts(packs, in)
	if len(refused) != 1 || refused[0].Name != "acme-svc" || refused[0].Pack != "local" ||
		!strings.Contains(refused[0].Why, "not one yolo ships") {
		t.Fatalf("refused %+v, want the local pack's acme-svc host half, named with OQ-HS4's reason", refused)
	}
	hostHalf := map[string]bool{}
	for _, s := range out {
		hostHalf[s.Name] = s.HostHalf
	}
	if hostHalf["acme-svc"] || !hostHalf["wire-bridge"] || hostHalf["a-loophole"] {
		t.Errorf("host halves after admission %v, want only the official bridge's kept", hostHalf)
	}
	for _, s := range in {
		if s.Name == "acme-svc" && !s.HostHalf {
			t.Error("AdmitServiceHosts edited its input's spec")
		}
	}
}
