package entrypoint

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

// loadMCPSignIn decodes the production launch wire without reading credentials
// or reconstructing placement. Nil means absent legacy input; a zero-version
// value preserves supplied-invalid input so it cannot regain legacy eligibility.
func (e *Env) loadMCPSignIn() *mcpsignin.Launch {
	raw := e.Getenv(mcpsignin.WireEnv)
	if raw == "" {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var launch mcpsignin.Launch
	if err := decoder.Decode(&launch); err != nil || launch.Version != mcpsignin.LaunchVersion {
		e.warnOnce("MCP sign-in facts are unreadable; agents keep native sign-in. Relaunch with a current yolo build.")
		return &mcpsignin.Launch{}
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		e.warnOnce("MCP sign-in facts are unreadable; agents keep native sign-in. Relaunch with a current yolo build.")
		return &mcpsignin.Launch{}
	}
	return &launch
}
