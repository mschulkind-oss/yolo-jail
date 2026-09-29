package awscredadapter

// doorway_test.go pins DoorwayMain's body (doorwayPrepare): the AWS credential DOORWAY a
// macos-user launch opens outside its sandbox (docs/design/host-notch-services.md HS-D15) is this
// package's Handler, fed from the launch's input: the caller token the launch minted, and the
// endpoint file of the session's aws-auth front, which it forwards through as the jail's copy does.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func doorwayServe(t *testing.T, input map[string]string, request func(string, any, io.Writer) (Answer, error)) string {
	t.Helper()
	serve, err := doorwayPrepare(request)(func(k string) string { return input[k] })
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serve(l) }()
	t.Cleanup(func() { _ = l.Close(); <-done })
	return l.Addr().String()
}

func getCredentials(t *testing.T, addr, auth string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+CredentialsPath, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestTheAWSDoorwayForwardsThroughTheSessionsEndpointBehindTheLaunchsToken(t *testing.T) {
	var askedEndpoint string
	var asked []any
	request := func(endpoint string, req any, _ io.Writer) (Answer, error) {
		askedEndpoint = endpoint
		asked = append(asked, req)
		body, _ := json.Marshal(liveCredential().ContainerCredentials())
		return Answer{Body: body, OK: true}, nil
	}
	addr := doorwayServe(t, map[string]string{
		CallerTokenEnv: testToken, EndpointEnv: "/session/aws-auth.endpoint",
	}, request)
	if code, _ := getCredentials(t, addr, ""); code != http.StatusUnauthorized {
		t.Errorf("a request without the caller token got %d, want 401", code)
	}
	if len(asked) != 0 {
		t.Errorf("the host service was asked for an unauthenticated caller: %v", asked)
	}
	code, out := getCredentials(t, addr, testToken)
	if code != http.StatusOK || out["AccessKeyId"] == nil {
		t.Fatalf("a request with the token got %d %v, want the credential body", code, out)
	}
	if askedEndpoint != "/session/aws-auth.endpoint" {
		t.Errorf("the doorway asked through %q, want the endpoint the launch handed it", askedEndpoint)
	}
}

// NO ENDPOINT IS SERVED, NOT REFUSED: the host service did not start (it refuses at spawn without
// a configured profile, and the launch goes on), so each request is answered with the 4xx naming
// the host log, exactly as the jail's copy answers — the real Request refuses an empty endpoint.
func TestTheAWSDoorwayWithNoEndpointAnswersEachRequestWithTheReason(t *testing.T) {
	addr := doorwayServe(t, map[string]string{CallerTokenEnv: testToken}, Request)
	code, out := getCredentials(t, addr, testToken)
	if code != http.StatusBadRequest || out["Code"] != "ServiceUnreachable" {
		t.Errorf("got %d %v, want 400 ServiceUnreachable", code, out)
	}
}

func TestTheAWSDoorwayRefusesToStartWithoutItsToken(t *testing.T) {
	if _, err := doorwayPrepare(Request)(func(string) string { return "" }); err == nil {
		t.Error("the doorway prepared with no caller token")
	}
}
