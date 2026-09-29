package openaiauthhost

// doorway_test.go pins DoorwayMain's body (doorwayPrepare): the Codex refresh DOORWAY a
// macos-user launch opens outside its sandbox (docs/design/host-notch-services.md HS-D15) is
// serveAdapter, the adapter `yolo host -- codex` serves, fed from the launch's input: the caller
// token the Codex launcher binds into the refresh marker, and the host broker's private socket.

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
)

func postCodexRefresh(t *testing.T, addr, refreshToken string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "refresh_token": refreshToken})
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Post("http://"+addr+"/oauth/token",
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestTheCodexDoorwayServesTheHostAdapterFromTheLaunchsInput(t *testing.T) {
	token := strings.Repeat("ef", 32)
	var asked []map[string]any
	var askedSocket string
	request := func(socket string, req any, _ io.Writer) (json.RawMessage, error) {
		askedSocket = socket
		asked = append(asked, req.(map[string]any))
		return codexViewFixture(), nil
	}
	input := map[string]string{
		openauthclient.CallerTokenEnv: token,
		openauthclient.HostSocketEnv:  "/tmp/broker.host",
	}
	serve, err := doorwayPrepare(request, io.Discard)(func(k string) string { return input[k] })
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serve(l) }()
	defer func() { _ = l.Close(); <-done }()

	if code, _ := postCodexRefresh(t, l.Addr().String(), "yolo-broker:1"); code != http.StatusUnauthorized {
		t.Errorf("a refresh with no caller token got %d, want 401", code)
	}
	if len(asked) != 0 {
		t.Errorf("the broker was asked for a refresh the doorway refused: %v", asked)
	}
	code, out := postCodexRefresh(t, l.Addr().String(), openauthclient.BindCallerToken("yolo-broker:1", token))
	if code != http.StatusOK {
		t.Fatalf("a refresh with the launch's token got %d %v", code, out)
	}
	if len(asked) != 1 || asked[0]["action"] != "refresh" || asked[0]["refresh_token"] != "yolo-broker:1" ||
		askedSocket != "/tmp/broker.host" {
		t.Errorf("the broker was asked %v on %q, want one refresh of the stripped marker on the input's socket",
			asked, askedSocket)
	}
	if rt, _ := out["refresh_token"].(string); rt != openauthclient.BindCallerToken("yolo-broker:7", token) {
		t.Errorf("the answered marker %q is not bound to the launch's token again", rt)
	}
}

func TestTheCodexDoorwayRefusesToStartWithoutItsInputs(t *testing.T) {
	for name, input := range map[string]map[string]string{
		"no caller token": {openauthclient.HostSocketEnv: "/tmp/broker.host"},
		"no socket":       {openauthclient.CallerTokenEnv: strings.Repeat("ef", 32)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := doorwayPrepare(nil, io.Discard)(func(k string) string { return input[k] })
			if err == nil {
				t.Error("the doorway prepared without an input it needs")
			}
		})
	}
}
