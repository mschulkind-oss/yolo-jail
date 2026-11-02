package serialdaemon

import (
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// The serial loophole's grant, as its manifest declares it (plan F4): one valued kind whose
// values are device globs. The grant block carries standings and values, not the manifest's
// `access` or `value_noun`, so the daemon spells them here for the refusal.
const (
	grantPack   = "serial"
	devicesKind = "devices"
	grantAccess = "open host serial devices"
	grantNoun   = "device globs"
	// refusedExit is a grant refusal's exit code (plan PD-7), the brokers' and gh's.
	refusedExit = loopholegrant.RefusedExit
)

// refusal is why cfg refuses a request naming device ("" for `list`), or ok false when it may
// run. Without a grant block nothing is refused here (plan PD-12): the legacy allowlist check
// in each handler still applies. Under one, a kind that is not granted refuses every mode, and
// a granted kind refuses a device outside its globs, naming that device as the value to ask for.
func (cfg Settings) refusal(device string) (loopholegrant.Refusal, bool) {
	b := cfg.Grants
	if b == nil {
		return loopholegrant.Refusal{}, false
	}
	r := loopholegrant.Refusal{
		Client: "yolo-serial", Pack: b.Pack, Access: grantAccess, Kind: devicesKind,
		ValueNoun: grantNoun, Dir: b.Workspace, Standing: loopholegrant.NotAllowed,
	}
	if r.Pack == "" {
		r.Pack = grantPack
	}
	k, declared := b.Kinds[devicesKind]
	switch {
	case !declared:
		return r, true
	case k.Standing != loopholegrant.Granted:
		r.Standing = k.Standing
		r.Values = k.Values
		return r, true
	case device != "" && !isDeviceAllowed(device, cfg.AllowedDevices):
		r.Standing = loopholegrant.Granted
		r.Values = []string{device}
		return r, true
	}
	return loopholegrant.Refusal{}, false
}

// refused writes cfg's refusal for device to the session and reports whether it did.
func refused(s *hostservice.Session, cfg Settings, device string) bool {
	r, refuse := cfg.refusal(device)
	if !refuse {
		return false
	}
	s.Stderr(r.Text())
	s.Exit(refusedExit)
	return true
}
