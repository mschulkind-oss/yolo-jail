package serialdaemon

import (
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// grants.go is the daemon's whole decision (docs/design/loophole-access-approval.md §4.3, §4.6,
// §5; plan T9, F4, PD-12).
//
// # The grant file is the allowlist
//
// The file --settings names is this loophole's grant file, and the one kind, `devices`, is the
// whole decision: its granted values are the device globs yolo-serial may list and open, and a
// device outside them is refused, naming that device as the value to ask for. Any other standing
// refuses every mode, exit 64 (PD-7), with loopholegrant's §5 text naming the next step.
//
// The loophole is flipped (PD-12): there is no `allowed_devices` setting and no default glob any
// more, and a file with no `grants` key, no file, or no --settings at all grants nothing. Every
// request then fails closed with the not_allowed text. An `allowed_devices` key an older launch
// left in the file is never read.
//
// # The block is re-read at each request; the settings are not
//
// `yolo config apply` rewrites a running jail's grant file and says the change is applied now
// (plan T11), so each request reads the block afresh, as the journal and host-processes daemons
// do. `default_baud` stays frozen at startup (settings.go). Re-reading is safe because the grant
// file sits in the loophole's private state dir, outside every mount, written only from a gate's
// approved result. A block that stops reading, or vanishes, fails closed.

// The grant this daemon serves. Access and the value noun are the manifest's own words, which the
// launch writes into the grant file; these are the fallback for a file that does not carry them
// (one an older launch wrote, or none at all).
const (
	grantPack   = "serial"
	devicesKind = "devices"
	grantAccess = "open host serial devices"
	grantNoun   = "device globs"
	// refusedExit is a grant refusal's exit code (plan PD-7), the brokers' and gh's.
	refusedExit = loopholegrant.RefusedExit
)

// access is one request's reading of the grant file.
type access struct {
	block loopholegrant.Block
	kind  loopholegrant.Kind
	// declared is whether the block names the devices kind at all.
	declared bool
}

// readAccess reads the grant block at path. An empty path, a missing file, a file with no block
// and a block that does not read all come back as a block granting nothing.
func readAccess(path string) access {
	var a access
	if path == "" {
		return a
	}
	if _, b, err := loopholegrant.Read(path); err == nil && b != nil {
		a.block = *b
		a.kind, a.declared = b.Kinds[devicesKind]
	}
	return a
}

// devices is the granted globs, nil when the kind is not granted.
func (a access) devices() []string {
	if !a.declared || a.kind.Standing != loopholegrant.Granted {
		return nil
	}
	return a.kind.Values
}

// refusal is why a request naming device ("" for `list`) is refused, or ok false when it may
// run. A kind that is not granted refuses every mode; a granted kind refuses a device outside its
// globs, naming that device as the value to ask for.
func (a access) refusal(device string) (loopholegrant.Refusal, bool) {
	r := loopholegrant.Refusal{
		Client: "yolo-serial", Pack: a.block.Pack, Access: a.block.Access, Kind: devicesKind,
		ValueNoun: a.kind.ValueNoun, Dir: a.block.Workspace, Standing: loopholegrant.NotAllowed,
	}
	if r.Pack == "" {
		r.Pack = grantPack
	}
	if r.Access == "" {
		r.Access = grantAccess
	}
	if r.ValueNoun == "" {
		r.ValueNoun = grantNoun
	}
	switch {
	case !a.declared:
		return r, true
	case a.kind.Standing != loopholegrant.Granted:
		r.Standing = a.kind.Standing
		r.Values = a.kind.Values
		return r, true
	case device != "" && !isDeviceAllowed(device, a.kind.Values):
		r.Standing = loopholegrant.Granted
		r.Values = []string{device}
		return r, true
	}
	return loopholegrant.Refusal{}, false
}

// refused writes a's refusal for device to the session and reports whether it did.
func refused(s *hostservice.Session, a access, device string) bool {
	r, refuse := a.refusal(device)
	if !refuse {
		return false
	}
	s.Stderr(r.Text())
	s.Exit(refusedExit)
	return true
}
