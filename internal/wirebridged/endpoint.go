package wirebridged

// endpoint.go publishes the bridge's endpoint file — the liveness marker whose
// appearance (after the bind, §5's startup order) means a listener exists at
// the address it names.
//
// IT IS DELIBERATELY NOT svcendpoint.Publish. That function's format is the
// loopback-TLS transport's credential triple — host:port, pinned certificate,
// per-jail bearer token — and the bridge serves PLAIN HTTP to its own jail by
// design: claude dials the provider's declared `http://` base URL verbatim
// (WB-D2), and WB-D4 rules inbound auth out because the jail IS the boundary.
// A TLS-format file here would carry a cert and a token that authorize and pin
// nothing, and the first svcendpoint.Dial against it would fail a handshake the
// listener was never going to speak. What survives the borrowing is the write
// discipline the credential file earned: a 0700 directory, a temp file renamed
// into place (a client re-reading the path mid-write must never see a torn
// line), and a 0600 file.
//
// Nobody is required to read this file in this build — it is the §5 marker and
// the join point for the reachability witness (§5's "for free" coverage needs a
// consumer that watches it, which lands with the pack, wire-bridge-plan step 4).

import (
	"os"
	"path/filepath"
)

func publishEndpoint(path, addr string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".endpoint-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(e error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return e
	}
	if _, err := tmp.WriteString(addr + "\n"); err != nil {
		return cleanup(err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
