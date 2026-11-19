package mcpsignin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCallbackObserveRequiresProducedReadinessAndMatchingLease(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	lease := CallbackLease{Version: 1, Port: 3118, Launch: "owner-launch", Runtime: "podman", Generation: "generation"}
	write := func(path string, value any) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(CallbackLeasePath(root, 3118), lease)
	launch := &Launch{Version: LaunchVersion, CallbackGeneration: lease.Generation}
	row := Callback{Server: "configured-alias", Agent: "fixture-agent", Port: 3118, Owner: lease.Launch, Disposition: "publication pending"}
	receipt := CallbackReceipt{Version: 1, Generation: lease.Generation, Callbacks: []Callback{row}}
	write(CallbackReceiptPath(root, lease.Generation), receipt)
	if got := ObserveCallback(root, launch, row.Server); got.Disposition != "publication pending" {
		t.Fatal("plain reader promoted pending", got)
	}
	receipt.Callbacks[0].Disposition = "owner"
	write(CallbackReceiptPath(root, lease.Generation), receipt)
	if got := ObserveCallback(root, launch, row.Server); got.Disposition != "owner" {
		t.Fatal(got)
	}
	lease.Generation = "replacement"
	write(CallbackLeasePath(root, 3118), lease)
	if got := ObserveCallback(root, launch, row.Server); got.Disposition != "unavailable" || got.Owner != "" {
		t.Fatal("stale generation asserted ownership", got)
	}
	if err := os.Remove(CallbackLeasePath(root, 3118)); err != nil {
		t.Fatal(err)
	}
	if got := ObserveCallback(root, launch, row.Server); got.Disposition != "unavailable" {
		t.Fatal(got)
	}
	if _, err := os.Stat(CallbackLeasePath(root, 3118)); !os.IsNotExist(err) {
		t.Fatal("plain read created lease")
	}
}

func TestCallbackObserveUnsafeUnknownOrMalformedFactsAreUnrepaired(t *testing.T) {
	for _, body := range []string{`{"version":2}`, `{"version":2,"version":1,"generation":"generation","callbacks":[]}`, `{"version":1,"generation":"generation","callbacks":[],"extra":"private-do-not-print"}`, `{`} {
		root := filepath.Join(t.TempDir(), "private")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		path := CallbackReceiptPath(root, "generation")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		launch := &Launch{Version: LaunchVersion, CallbackGeneration: "generation"}
		if got := ObserveCallback(root, launch, "configured-alias"); got.Disposition != "unavailable" || got.Owner != "" {
			t.Fatal(got)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != body {
			t.Fatal("plain reader repaired metadata")
		}
	}
}
