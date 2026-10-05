package main

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE CLIENT READS THE ONE SPELLING THE macos-user STAGER KEYS ON. paths.SerialEndpointEnv is
// what the launch reads to decide that this sandbox needs yolo-serial (macosuser.GuestClients),
// and what this client reads to find the endpoint, so a client that read any other variable
// would be staged for an endpoint it never dials. Fails if resolveEndpoint stops reading it.
func TestTheClientReadsTheSharedSerialEndpointVariable(t *testing.T) {
	if paths.SerialEndpointEnv != "YOLO_SERVICE_SERIAL_ENDPOINT" {
		t.Fatalf("paths.SerialEndpointEnv = %q; the run pipeline publishes the serial loophole's "+
			"endpoint as YOLO_SERVICE_SERIAL_ENDPOINT", paths.SerialEndpointEnv)
	}
	t.Setenv(paths.SerialEndpointEnv, "/run/yolo-services/serial.endpoint")
	got, err := resolveEndpoint("")
	if err != nil || got != "/run/yolo-services/serial.endpoint" {
		t.Errorf("resolveEndpoint(\"\") = %q, %v; want the value of %s", got, err, paths.SerialEndpointEnv)
	}
	if got, _ := resolveEndpoint("/custom"); got != "/custom" {
		t.Errorf("--endpoint no longer wins over the variable: %q", got)
	}
	t.Setenv(paths.SerialEndpointEnv, "")
	if _, err := resolveEndpoint(""); err == nil {
		t.Error("an empty variable resolved to an endpoint")
	}
}
