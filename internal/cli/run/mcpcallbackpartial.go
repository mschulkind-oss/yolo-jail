package run

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

// stopOwnedMCPCallbackPartial retires only this failed attempt's immutable
// runtime ID. It leaves services, source poll, inputs, staging and leases intact.
// Its caller has already drained the client and settled the running observer.
func (k *keeper) stopOwnedMCPCallbackPartial(id string) bool {
	o, p := k.o, k.plan
	if k.jm == nil {
		return false
	}
	select {
	case <-k.jm.exited:
	default:
		return false
	}
	k.lockMu.Lock()
	if k.launchLock == nil {
		k.lockMu.Unlock()
		return false
	}
	if _, err := k.launchLock.Stat(); err != nil {
		lock, acquired := tryWorkspaceLock(p.Cname)
		if !acquired {
			k.lockMu.Unlock()
			return false
		}
		k.launchLock = lock.f
		k.lockOnce = sync.Once{}
	}
	k.lockMu.Unlock()
	for _, expected := range p.CallbackLeases {
		lease, err := mcpsignin.ReadCallbackLease(o.callbackRoot(), expected.Port)
		if err != nil || lease != expected || lease.Generation != o.mcpCallbackGeneration {
			return false
		}
	}
	expected, present := "", false
	for i := 0; i+1 < len(k.callbackRunCmd); i++ {
		if k.callbackRunCmd[i] == "-e" && strings.HasPrefix(k.callbackRunCmd[i+1], mcpsignin.WireEnv+"=") {
			if present {
				return false
			}
			expected = strings.TrimPrefix(k.callbackRunCmd[i+1], mcpsignin.WireEnv+"=")
			present = true
		}
	}
	lines, readable := o.inspectContainerEnv(p.Runtime, id)
	matches := 0
	for _, line := range lines {
		if strings.HasPrefix(line, mcpsignin.WireEnv+"=") {
			if line != mcpsignin.WireEnv+"="+expected {
				return false
			}
			matches++
		}
	}
	if !present || !readable || matches != 1 {
		return false
	}
	current, known := o.probeExistingContainer(p.Cname, p.Runtime, trackingProbeTimeout)
	if !known || current != id {
		return false
	}
	k.sink.logf("keeper: stopping this failed optional-callback attempt before retrying without the affected publication; native sign-in can use the full callback URL paste.")
	o.recordJailStop(p.Cname, "failed optional native callback publication; retrying without that convenience")
	o.Exec([]string{p.Runtime, "stop", "-t", strconv.Itoa(TeardownStopTimeoutSeconds), id}, "", nil, time.Duration(TeardownStopTimeoutSeconds+5)*time.Second)
	current, known = o.probeExistingContainer(p.Cname, p.Runtime, trackingProbeTimeout)
	if known && current == "" {
		return true
	}
	if !known || current != id {
		return false
	}
	running, known := o.probeRunningContainer(p.Cname, p.Runtime, trackingProbeTimeout)
	if !known || running != "" {
		return false
	}
	// Ordinary keeper leftover cleanup, addressed to the proven immutable ID;
	// its exit code is not absence evidence. Recheck the name after removal.
	o.Exec([]string{p.Runtime, "rm", "--force", id}, "", nil, trackingProbeTimeout)
	current, known = o.probeExistingContainer(p.Cname, p.Runtime, trackingProbeTimeout)
	return known && current == ""
}
