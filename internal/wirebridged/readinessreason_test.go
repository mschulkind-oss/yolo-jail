package wirebridged

// readinessreason_test.go pins the bridge's `failed` reason as the boot's refusal reads it
// (docs/reference/loopback-tls-reachability.md OQ-R8): the entrypoint prints the reason
// verbatim as the refusal's cause, so the reason is where the cause is named in plain words
// and where its next step is given. Each case drives serve() and reads the readiness pipe,
// the bytes the boot actually receives (readinessPipe, hosthalf_test.go).

import (
	"context"
	"net"
	"strings"
	"testing"
)

// A HELD PORT is named as one, with what to do about it, ahead of the bind error and the holder.
func TestAHeldPortsReadinessReasonNamesThePortAndTheNextStep(t *testing.T) {
	line := readinessPipe(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	addr := held.Addr().String()
	_, port, _ := net.SplitHostPort(addr)

	captureDiag(t)
	if rc := serve(context.Background(), route{ProviderName: "cerebras", ListenAddr: addr,
		UpstreamBaseURL: "https://upstream.example/v1"}, tokenEnv(map[string]string{})); rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	got := <-line
	for _, want := range []string{
		"port " + port + " is already taken", // the cause, in plain words
		"address already in use",             // the bind error, verbatim
		"free it",                            // the next step
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the readiness reason does not say %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\n") {
		t.Errorf("the readiness record spans more than one line: %q", got)
	}
}

// A MISSING KEY names the provider and the variable, and where to put it, rather than
// "provider credential is unavailable", which named neither.
func TestAMissingKeysReadinessReasonNamesTheVariableAndWhereToPutIt(t *testing.T) {
	const key = "YOLO_TEST_BRIDGE_ABSENT_KEY"
	t.Setenv(key, "")
	line := readinessPipe(t)
	captureDiag(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the idle that follows the report returns at once
	serve(ctx, route{ProviderName: "cerebras", ListenAddr: "127.0.0.1:0",
		UpstreamBaseURL: "https://upstream.example/v1", KeyEnvName: key},
		tokenEnv(map[string]string{"JAIL_HOME": t.TempDir()}))
	got := <-line
	for _, want := range []string{`provider "cerebras"`, key, "env_sources"} {
		if !strings.Contains(got, want) {
			t.Errorf("the readiness reason does not name %q: %q", want, got)
		}
	}
}
