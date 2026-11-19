package run

import (
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

// startupCapture retains a bounded pre-ready runtime observation for closed
// classification. Raw text is never copied into callback status/receipts.
type startupCapture struct {
	mu   sync.Mutex
	body []byte
}

func (s *startupCapture) Write(body []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining := 32768 - len(s.body)
	if remaining > len(body) {
		remaining = len(body)
	}
	if remaining > 0 {
		s.body = append(s.body, body[:remaining]...)
	}
	return len(body), nil
}

func (s *startupCapture) conflicts(leases []mcpsignin.CallbackLease) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return callbackBindConflicts(string(s.body), leases)
}

func callbackBindConflicts(text string, leases []mcpsignin.CallbackLease) []int {
	if len(text) > 32768 {
		text = text[:32768]
	}
	text = strings.ToLower(text)
	var ports []int
	for _, lease := range leases {
		endpoint := regexp.MustCompile(`127\.0\.0\.1:` + strconv.Itoa(lease.Port) + `($|[^0-9])`)
		for _, line := range strings.Split(text, "\n") {
			conflict := strings.Contains(line, "address already in use") || strings.Contains(line, "port is already allocated")
			if conflict && endpoint.MatchString(line) {
				ports = append(ports, lease.Port)
				break
			}
		}
	}
	return ports
}

var keeperStartJailMain = startJailMain

func (k *keeper) startMCPCallbackAttempt(argv []string) (*jailMain, error) {
	return keeperStartJailMain(argv, keeperStream{k.sink, frameJailStdout}, keeperStream{k.sink, frameJailStderr}, func() { k.o.Perf.Mark("jail_main.exited") })
}

// retryMCPCallbackFailure requires both an observed optional bind conflict and
// confirmed runtime absence. Services, inputs, staging and the source poll remain
// owned by this keeper; only the failed client/observation attempt is replaced.
func (k *keeper) retryMCPCallbackFailure(ports []int, stage string) bool {
	if len(ports) == 0 || k.plan.LaunchInputMCP == nil || k.plan.LaunchInputMCP.SignIn == nil {
		return false
	}
	o, p := k.o, k.plan
	select {
	case <-k.lifelineGone:
		return false
	case <-k.ending:
		return false
	default:
	}
	facts := p.LaunchInputMCP.SignIn
	for _, port := range ports {
		_ = writeCallbackJSON(mcpsignin.CallbackReceiptPath(o.callbackRoot(), facts.CallbackGeneration)+".failure-"+strconv.Itoa(port), map[string]any{"version": 1, "generation": facts.CallbackGeneration, "port": port, "stage": stage, "code": "optional-port-conflict"})
	}
	id, known := o.probeExistingContainer(p.Cname, p.Runtime, trackingProbeTimeout)
	if !known || (id != "" && !k.stopOwnedMCPCallbackPartial(id)) {
		k.callbackPreserveRuntime = true
		k.sink.logf("keeper: optional callback retry is unsafe because runtime ownership or absence is not confirmed; retaining ownership. Run yolo check before stopping the owning jail and relaunching; native sign-in still supports pasting the full callback URL.")
		return false
	}
	var removed []mcpsignin.CallbackLease
	for _, lease := range p.CallbackLeases {
		if slices.Contains(ports, lease.Port) {
			removed = append(removed, lease)
		}
	}
	if len(removed) == 0 {
		return false
	}
	for _, lease := range removed {
		o.releaseCallbackLease(lease)
		for i := range facts.Callbacks {
			row := &facts.Callbacks[i]
			if row.Port == lease.Port && row.Owner == lease.Launch {
				row.Disposition, row.Owner = "port busy", ""
			}
		}
	}
	p.CallbackLeases = slices.DeleteFunc(p.CallbackLeases, func(lease mcpsignin.CallbackLease) bool { return slices.Contains(ports, lease.Port) })
	o.mcpCallbackLeases = slices.Clone(p.CallbackLeases)
	p.RunCmd = removeMCPCallbackPublications(p.RunCmd, removed)
	k.callbackRunCmd = removeMCPCallbackPublications(k.callbackRunCmd, removed)
	_ = o.writeCallbackReceipt(facts.CallbackGeneration, facts.Callbacks)
	k.sink.logf("keeper: optional native callback port busy; continuing without the affected publication. Paste the full callback URL into the agent's prompt, or free the port and relaunch.")
	jm, err := k.startMCPCallbackAttempt(k.callbackRunCmd)
	if err != nil {
		return false
	}
	k.jm = jm
	k.running = make(chan struct{})
	go k.awaitRunning(jm, k.running, len(p.CallbackLeases) > 0)
	return true
}

func removeMCPCallbackPublications(argv []string, removed []mcpsignin.CallbackLease) []string {
	out := slices.Clone(argv)
	for _, lease := range removed {
		port := strconv.Itoa(lease.Port)
		spec := "127.0.0.1:" + port + ":" + port + "/tcp"
		for i := 0; i+1 < len(out); i++ {
			if out[i] == "-p" && out[i+1] == spec {
				out = slices.Delete(out, i, i+2)
				break
			}
		}
		for i := 0; i+1 < len(out); i++ {
			if out[i] != "-e" || !strings.HasPrefix(out[i+1], "YOLO_PUBLISHED_PORTS=") {
				continue
			}
			var ports []string
			if json.Unmarshal([]byte(strings.TrimPrefix(out[i+1], "YOLO_PUBLISHED_PORTS=")), &ports) == nil && !publicationTargets(out, port+"/tcp") {
				ports = slices.DeleteFunc(ports, func(value string) bool { return value == port+"/tcp" })
				out[i+1] = "YOLO_PUBLISHED_PORTS=" + jsonDumpsStrings(ports)
			}
		}
	}
	if !slices.Contains(out, "-p") {
		for i := 0; i+1 < len(out); i++ {
			if out[i] == "--sysctl" && out[i+1] == "net.ipv4.conf.all.route_localnet=1" {
				out = slices.Delete(out, i, i+2)
				break
			}
		}
	}
	return out
}

func publicationTargets(argv []string, target string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-p" {
			continue
		}
		spec := argv[i+1]
		proto := "tcp"
		if at := strings.LastIndex(spec, "/"); at >= 0 {
			proto, spec = spec[at+1:], spec[:at]
		}
		parts := strings.Split(spec, ":")
		if parts[len(parts)-1]+"/"+proto == target {
			return true
		}
	}
	return false
}

func (k *keeper) confirmMCPCallbackPublications() {
	facts := k.o.launchInputMCP.SignIn
	if facts == nil || facts.CallbackGeneration == "" {
		return
	}
	for i := range facts.Callbacks {
		row := &facts.Callbacks[i]
		if row.Disposition != "publication pending" {
			continue
		}
		lease, err := mcpsignin.ReadCallbackLease(k.o.callbackRoot(), row.Port)
		if err == nil && lease.Generation == facts.CallbackGeneration && lease.Launch == k.plan.Cname {
			row.Disposition = "owner"
		}
	}
	_ = k.o.writeCallbackReceipt(facts.CallbackGeneration, facts.Callbacks)
}

var _ io.Writer = (*startupCapture)(nil)
