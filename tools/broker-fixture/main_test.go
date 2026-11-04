package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixtureBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "broker-fixture")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	return bin
}
func runBinary(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()
	return runBinaryTimeout(t, 10*time.Second, bin, nil, args...)
}
func runBinaryTimeout(t *testing.T, timeout time.Duration, bin string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), ctx.Err()
	}
	return string(out), err
}
func startFixture(t *testing.T, bin, dir string) fixtureMeta {
	t.Helper()
	out, err := runBinary(t, bin, "start", "--state-dir", dir)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(synthetic)") || !strings.Contains(out, "endpoint: http://127.0.0.1:") || !strings.Contains(out, "state: "+dir) {
		t.Fatalf("start did not disclose only endpoint, synthetic identity and state path: %q", out)
	}
	m, err := readMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, m.ClientToken) || strings.Contains(out, m.AdminToken) {
		t.Fatal("start printed a synthetic credential")
	}
	t.Cleanup(func() {
		if !fixtureListenerAccepts(m.Endpoint) {
			return
		}
		out, err := runBinary(t, bin, "stop", "--state-dir", dir)
		if err != nil {
			t.Errorf("stop fixture through its state directory: %v %s", err, out)
			if stopErr := requestStopFixture(m); stopErr != nil {
				t.Errorf("stop original fixture endpoint for cleanup: %v", stopErr)
				return
			}
		}
		if err := waitListenerStopped(m.Endpoint); err != nil {
			t.Errorf("fixture listener did not stop: %s: %v", m.Endpoint, err)
		}
	})
	return m
}
func postJSON(t *testing.T, endpoint, path string, headers map[string]string, value any) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(value)
	req, err := http.NewRequest(http.MethodPost, endpoint+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}
func TestFixtureHostControlsAndGraphQLSelection(t *testing.T) {
	bin := fixtureBinary(t)
	dir := filepath.Join(t.TempDir(), "run-a")
	m := startFixture(t, bin, dir)
	privateInfo, err := os.Stat(filepath.Join(dir, "private.json"))
	if err != nil {
		t.Fatal(err)
	}
	if privateInfo.Mode().Perm() != 0600 {
		t.Fatalf("credentials are not private: %o", privateInfo.Mode().Perm())
	}
	keyInfo, err := os.Stat(filepath.Join(dir, "synthetic-signing-key"))
	if err != nil || keyInfo.Mode().Perm() != 0600 {
		t.Fatalf("synthetic signing key is not private: %v %v", err, keyInfo)
	}
	if out, err := runBinary(t, bin, "seed", "--state-dir", dir); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	unauthorized, _ := postJSON(t, m.Endpoint, "/graphql", nil, map[string]string{"query": `mutation Unauthorized { updateWorkItem(id:"a1", state:"bad") { id } }`})
	if unauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("service accepted a write without its synthetic credential: %d", unauthorized.StatusCode)
	}
	out, err := runBinary(t, bin, "client", "--state-dir", dir, "list")
	if err != nil || !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("synthetic CLI read: %v %s", err, out)
	}
	out, err = runBinary(t, bin, "client", "--state-dir", dir, "get", "a1")
	if err != nil || !strings.Contains(out, `"project":"alpha"`) {
		t.Fatalf("synthetic CLI lookup: %v %s", err, out)
	}
	out, err = runBinary(t, bin, "client", "--state-dir", dir, "update", "b1", "in-progress")
	if err != nil || !strings.Contains(out, `"id":"b1"`) {
		t.Fatalf("synthetic CLI write: %v %s", err, out)
	}
	query := `query Ignored { item(id:"b1") { id } } query Named { first: project(id:"alpha") { items { id state } } }`
	resp, body := postJSON(t, m.Endpoint, "/graphql", map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]string{"query": query, "operationName": "Named"})
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(`"first"`)) || !bytes.Contains(body, []byte(`"a1"`)) || bytes.Contains(body, []byte(`"b1"`)) {
		t.Fatalf("named aliased query not selected: status=%d body=%s", resp.StatusCode, body)
	}
	mutation := `mutation Change { one: updateWorkItem(id:"a1", state:"done") { id state } }`
	resp, body = postJSON(t, m.Endpoint, "/graphql", map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]string{"query": mutation, "operationName": "Change"})
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(`"one"`)) {
		t.Fatalf("selected mutation failed: %d %s", resp.StatusCode, body)
	}
	// A valid first mutation followed by an unknown field is rejected atomically.
	bad := `mutation Bad { updateWorkItem(id:"a1", state:"bad") { id } nope { id } }`
	for _, doc := range []string{bad, `subscription Watch { item(id:"a1") { id } }`, `query A { item(id:"a1") { id } } query B { item(id:"b1") { id } }`} {
		resp, _ = postJSON(t, m.Endpoint, "/graphql", map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]string{"query": doc})
		if resp.StatusCode == 200 {
			t.Fatalf("unsupported GraphQL operation accepted: %s", doc)
		}
	}
	resp, _ = postJSON(t, m.Endpoint, "/graphql", map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]string{"query": `mutation Unknown { updateWorkItem(id:"a1", state:"bad") { mystery } }`})
	if resp.StatusCode == 200 {
		t.Fatal("unknown nested selection accepted")
	}
	conflict := `query Same { item(id:"a1") { id } } mutation Write { updateWorkItem(id:"a1", state:"conflict") { id } }`
	resp, _ = postJSON(t, m.Endpoint, "/graphql?operationName=Same", map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]string{"query": conflict, "operationName": "Write"})
	conflictAccepted := resp.StatusCode == http.StatusOK
	for _, selectors := range []struct {
		url, body string
	}{
		{"Write", "write"},
	} {
		resp, _ = postJSON(t, m.Endpoint, "/graphql?operationName="+selectors.url, map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]string{"query": conflict, "operationName": selectors.body})
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("case-mismatched GraphQL selector accepted: %+v", selectors)
		}
	}
	duplicate := `query Same { item(id:"a1") { id } } mutation Same { updateWorkItem(id:"a1", state:"duplicate") { id } }`
	resp, _ = postJSON(t, m.Endpoint, "/graphql?operationName=Same", map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]string{"query": duplicate, "operationName": "Same"})
	if resp.StatusCode == http.StatusOK {
		t.Fatal("duplicate GraphQL operation names were selected ambiguously")
	}
	out, err = runBinary(t, bin, "inspect", "--state-dir", dir)
	if err != nil {
		t.Fatalf("inspect fixture after rejected GraphQL calls: %v %s", err, out)
	}
	var afterInvalid fixtureState
	if err = json.Unmarshal([]byte(out), &afterInvalid); err != nil {
		t.Fatal(err)
	}
	if len(afterInvalid.Writes) != 2 || afterInvalid.Items[0].State != "done" || afterInvalid.Items[1].State != "in-progress" {
		t.Fatalf("rejected GraphQL call changed fixture state or write log (including conflicting selectors): %+v", afterInvalid)
	}
	if conflictAccepted {
		t.Fatal("contradictory GraphQL operationName selectors were accepted")
	}
	signingKey, err := os.ReadFile(filepath.Join(dir, "synthetic-signing-key"))
	if err != nil || bytes.Contains(signingKey, []byte(m.AdminToken)) || bytes.Contains(signingKey, []byte(m.ClientToken)) {
		t.Fatalf("synthetic signing key contains a control token: %v", err)
	}
	out, err = runBinary(t, bin, "change", "--state-dir", dir, "a1", "closed")
	if err != nil {
		t.Fatalf("host change: %v %s", err, out)
	}
	out, err = runBinary(t, bin, "inspect", "--state-dir", dir)
	if err != nil || !strings.Contains(out, "host-change") || !strings.Contains(out, "closed") || !strings.Contains(out, `"id":"b1","project":"beta","state":"in-progress"`) {
		t.Fatalf("inspect write log: %v %s", err, out)
	}
	if _, err = runBinary(t, bin, "reset", "--state-dir", dir); err != nil {
		t.Fatal(err)
	}
	out, err = runBinary(t, bin, "inspect", "--state-dir", dir)
	if err != nil || !strings.Contains(out, `"writes":[]`) || !strings.Contains(out, `"items":[]`) {
		t.Fatalf("reset did not clear private fixture state: %v %s", err, out)
	}
}
func TestFixturePinnedEndpointRejectsOverride(t *testing.T) {
	bin := fixtureBinary(t)
	dir := filepath.Join(t.TempDir(), "fixed")
	m := startFixture(t, bin, dir)
	if out, err := runBinary(t, bin, "seed", "--state-dir", dir); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	if out, err := runBinary(t, bin, "client", "--state-dir", dir, "--server=http://example.invalid", "list"); err == nil {
		t.Fatalf("fixture client accepted endpoint override: %s", out)
	}
	if out, err := runBinary(t, bin, "client", "--state-dir", dir, "--auth=forged", "list"); err == nil {
		t.Fatalf("fixture client accepted credential override: %s", out)
	}
	resp, _ := postJSON(t, m.Endpoint, "/_host/reset", map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("brokerable synthetic client gained host-admin authority: %d", resp.StatusCode)
	}
	// Stopping one fixture must not affect another listener or its private state.
	other := filepath.Join(t.TempDir(), "other")
	second := startFixture(t, bin, other)
	if second.ClientToken == m.ClientToken || second.AdminToken == m.AdminToken {
		t.Fatal("fixture runs reused synthetic credentials")
	}
	if out, err := runBinary(t, bin, "seed", "--state-dir", other); err != nil {
		t.Fatalf("seed second: %v %s", err, out)
	}
	if _, err := runBinary(t, bin, "stop", "--state-dir", dir); err != nil {
		t.Fatal(err)
	}
	if resp, err := http.Get(m.Endpoint + "/cli/projects"); err == nil {
		resp.Body.Close()
		t.Fatal("stop returned while its fixture listener still accepted connections")
	}
	deadline := time.Now().Add(time.Second)
	var err error
	for time.Now().Before(deadline) {
		resp, err = http.Get(second.Endpoint + "/cli/projects")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	req, _ := http.NewRequest(http.MethodGet, second.Endpoint+"/cli/projects", nil)
	req.Header.Set("Authorization", "Bearer "+second.ClientToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("second fixture was stopped with first: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("second listener unavailable: %s", resp.Status)
	}
}

func TestFixtureScenariosAreHostSelectedAndSynthetic(t *testing.T) {
	bin := fixtureBinary(t)
	dir := filepath.Join(t.TempDir(), "scenario")
	m := startFixture(t, bin, dir)
	if out, err := runBinary(t, bin, "scenario", "--state-dir", dir, "credential-echo"); err != nil {
		t.Fatalf("select synthetic echo: %v %s", err, out)
	}
	resp, body := postJSON(t, m.Endpoint, "/graphql", map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]string{"query": `query Q { item(id:"a1") { id } }`})
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte(m.ClientToken)) {
		t.Fatalf("host-selected synthetic echo scenario failed: %d %s", resp.StatusCode, body)
	}
	if _, err := runBinary(t, bin, "scenario", "--state-dir", dir, "lookup-failure"); err != nil {
		t.Fatal(err)
	}
	resp, body = postJSON(t, m.Endpoint, "/graphql", map[string]string{"Authorization": "Bearer " + m.ClientToken}, map[string]string{"query": `query Q { item(id:"a1") { id } }`})
	if resp.StatusCode != 503 || !strings.Contains(string(body), "synthetic lookup failure") {
		t.Fatalf("synthetic lookup-failure scenario: %d %s", resp.StatusCode, body)
	}
	if _, err := runBinary(t, bin, "scenario", "--state-dir", dir, "redirect"); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	queryBody, _ := json.Marshal(map[string]string{"query": `query Q { item(id:"a1") { id } }`})
	redirectReq, _ := http.NewRequest(http.MethodPost, m.Endpoint+"/graphql", bytes.NewReader(queryBody))
	redirectReq.Header.Set("Authorization", "Bearer "+m.ClientToken)
	redirectResp, err := client.Do(redirectReq)
	if err != nil {
		t.Fatal(err)
	}
	redirectResp.Body.Close()
	if redirectResp.StatusCode != http.StatusFound || redirectResp.Header.Get("Location") != m.RedirectEndpoint+"/sink" {
		t.Fatalf("synthetic redirect scenario: %s %q", redirectResp.Status, redirectResp.Header.Get("Location"))
	}
	control, _ := http.NewRequest(http.MethodPost, m.Endpoint+"/_host/inspect", nil)
	control.Header.Set("X-Fixture-Admin", m.AdminToken)
	controlResp, err := http.DefaultClient.Do(control)
	if err != nil {
		t.Fatal(err)
	}
	var redirectState fixtureState
	if err := json.NewDecoder(controlResp.Body).Decode(&redirectState); err != nil {
		t.Fatal(err)
	}
	controlResp.Body.Close()
	if len(redirectState.Writes) != 0 {
		t.Fatalf("redirect listener received a request: %+v", redirectState.Writes)
	}
	if _, err := runBinary(t, bin, "scenario", "--state-dir", dir, "oversize"); err != nil {
		t.Fatal(err)
	}
	largeReq, _ := http.NewRequest(http.MethodPost, m.Endpoint+"/graphql", bytes.NewReader(queryBody))
	largeReq.Header.Set("Authorization", "Bearer "+m.ClientToken)
	largeResp, err := http.DefaultClient.Do(largeReq)
	if err != nil {
		t.Fatal(err)
	}
	n, copyErr := io.Copy(io.Discard, largeResp.Body)
	largeResp.Body.Close()
	if copyErr != nil || n <= 32<<20 {
		t.Fatalf("oversize scenario returned %d bytes, err=%v", n, copyErr)
	}
}

func TestFixtureServeRetainsStateLockThroughRedirectTeardown(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "redirect-teardown")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	initial := fixtureMeta{Identity: fixtureIdentity, ClientToken: "synthetic-client", AdminToken: "synthetic-admin"}
	if err := writeJSON(filepath.Join(dir, "private.json"), initial, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "state.json"), fixtureState{Projects: []string{}, Items: []fixtureItem{}, Writes: []fixtureWrite{}}, 0600); err != nil {
		t.Fatal(err)
	}

	redirectEntered := make(chan struct{})
	releaseRedirect := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serveWithRedirectBeforeWrite([]string{"--state-dir", dir}, func() {
			enteredOnce.Do(func() { close(redirectEntered) })
			<-releaseRedirect
		})
	}()
	defer releaseOnce.Do(func() { close(releaseRedirect) })

	deadline := time.Now().Add(3 * time.Second)
	var meta fixtureMeta
	for time.Now().Before(deadline) {
		var err error
		meta, err = readMeta(dir)
		if err == nil && meta.Endpoint != "" && fixtureListenerReady(meta, 100*time.Millisecond) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if meta.Endpoint == "" || !fixtureListenerReady(meta, 100*time.Millisecond) {
		t.Fatal("fixture did not become ready")
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseRedirect) })
		_ = requestStopFixture(meta)
	})
	redirectDone := make(chan error, 1)
	go func() {
		resp, err := http.Get(meta.RedirectEndpoint + "/held-write")
		if err == nil {
			resp.Body.Close()
		}
		redirectDone <- err
	}()
	select {
	case <-redirectEntered:
	case <-time.After(time.Second):
		t.Fatal("redirect handler did not reach the controlled write boundary")
	}
	if err := requestStopFixture(meta); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && fixtureListenerAccepts(meta.Endpoint) {
		time.Sleep(time.Millisecond)
	}
	if fixtureListenerAccepts(meta.Endpoint) {
		t.Fatal("primary listener did not shut down")
	}

	// The redirect request is held before its state write; another direct serve must
	// remain excluded until shutdown has drained that handler.
	deadline = time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		lock, err := openStateLock(dir)
		if err != nil {
			t.Fatal(err)
		}
		err = lockStateFile(lock)
		_ = lock.Close()
		if err == nil {
			t.Fatal("state lock was released while the redirect handler could still write")
		}
		time.Sleep(time.Millisecond)
	}

	releaseOnce.Do(func() { close(releaseRedirect) })
	select {
	case err := <-redirectDone:
		if err != nil {
			t.Fatalf("redirect request: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("redirect request did not finish after release")
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not return after redirect teardown")
	}
	lock, err := openStateLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockStateFile(lock); err != nil {
		t.Fatalf("state lock remained held after complete teardown: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state fixtureState
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Writes) != 1 || state.Writes[0].Item != "/held-write" {
		t.Fatalf("redirect handler did not finish its state write before lock release: %+v", state.Writes)
	}
}

func TestFixtureForcedShutdownExitsAndRetainsLockUntilProcessExit(t *testing.T) {
	bin := fixtureBinary(t)
	dir := filepath.Join(t.TempDir(), "forced-deadline")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	meta := fixtureMeta{Identity: fixtureIdentity, ClientToken: "synthetic-client", AdminToken: "synthetic-admin"}
	if err := writeJSON(filepath.Join(dir, "private.json"), meta, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "state.json"), fixtureState{Projects: []string{}, Items: []fixtureItem{}, Writes: []fixtureWrite{}}, 0600); err != nil {
		t.Fatal(err)
	}
	holdPath := filepath.Join(dir, "held-writer")
	cmd := exec.Command(bin, "serve", "--state-dir", dir)
	cmd.Env = append(os.Environ(), "BROKER_FIXTURE_TEST_HOLD_REDIRECT_WRITE="+holdPath)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	defer func() {
		_ = os.WriteFile(holdPath+".release", []byte("release"), 0600)
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			select {
			case <-waitDone:
			case <-time.After(2 * time.Second):
			}
		}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		meta, err = readMeta(dir)
		if err == nil && meta.Endpoint != "" && fixtureListenerReady(meta, 100*time.Millisecond) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if meta.Endpoint == "" || !fixtureListenerReady(meta, 100*time.Millisecond) {
		t.Fatalf("fixture did not become ready: %s", output.String())
	}
	redirectDone := make(chan struct{})
	go func() {
		resp, err := http.Get(meta.RedirectEndpoint + "/held-writer")
		if err == nil {
			_ = resp.Body.Close()
		}
		close(redirectDone)
	}()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(holdPath + ".entered"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(holdPath + ".entered"); err != nil {
		t.Fatalf("writer did not hold the service mutex: %v", err)
	}
	if err := requestStopFixture(meta); err != nil {
		t.Fatal(err)
	}
	lock, err := openStateLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockStateFile(lock); err == nil {
		_ = lock.Close()
		t.Fatal("state lock was released while the old writer process was alive")
	}
	_ = lock.Close()

	select {
	case err := <-waitDone:
		if err == nil {
			t.Fatal("expired shutdown exited successfully despite a live writer")
		}
	case <-time.After(2500 * time.Millisecond):
		_ = os.WriteFile(holdPath+".release", []byte("release"), 0600)
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
		}
		t.Fatalf("fixture process did not fail closed within the shutdown deadline: %s", output.String())
	}
	if _, err := os.Stat(holdPath + ".release"); !os.IsNotExist(err) {
		t.Fatalf("test released the old writer before process termination: %v", err)
	}
	lock, err = openStateLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lockStateFile(lock); err != nil {
		t.Fatalf("state lock was not released after process termination: %v", err)
	}
	if !strings.Contains(output.String(), "shut down redirect fixture listener") {
		t.Fatalf("forced shutdown reason was not reported: %s", output.String())
	}
}

func TestFixtureCLIUpdateRejectsSaveFailure(t *testing.T) {
	dir := t.TempDir()
	original := fixtureState{
		Projects: []string{"alpha"},
		Items:    []fixtureItem{{ID: "a1", Project: "alpha", State: "open"}},
		Writes:   []fixtureWrite{},
	}
	if err := writeJSON(filepath.Join(dir, "state.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	s := &fixtureService{dir: dir, state: original, meta: fixtureMeta{ClientToken: "synthetic-client"}, stopping: true}
	req := httptest.NewRequest(http.MethodPost, "/cli/items/a1", strings.NewReader(`{"state":"done"}`))
	req.Header.Set("Authorization", "Bearer synthetic-client")
	resp := httptest.NewRecorder()
	s.cliAPI(resp, req)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("late CLI update response status = %d, want 500: %s", resp.Code, resp.Body.String())
	}
	if s.state.Items[0].State != "open" || len(s.state.Writes) != 0 {
		t.Fatalf("failed save changed in-memory state: %+v", s.state)
	}
	var persisted fixtureState
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || json.Unmarshal(b, &persisted) != nil {
		t.Fatalf("read persisted state: %v", err)
	}
	if persisted.Items[0].State != "open" || len(persisted.Writes) != 0 {
		t.Fatalf("failed save changed persisted state: %+v", persisted)
	}
}

func TestFixturePerStateDirectoryLifetimeLock(t *testing.T) {
	bin := fixtureBinary(t)
	dir := filepath.Join(t.TempDir(), "same-state")
	m := startFixture(t, bin, dir)
	if info, err := os.Lstat(filepath.Join(dir, "state.lock")); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Errorf("state lock is not a private regular file: info=%v err=%v", info, err)
	}
	if out, err := runBinary(t, bin, "start", "--state-dir", dir); err == nil {
		t.Fatalf("second start accepted an active state directory: %s", out)
	}
	out, err := runBinaryTimeout(t, 3*time.Second, bin, nil, "serve", "--state-dir", dir)
	if err == nil || err == context.DeadlineExceeded {
		t.Fatalf("direct second serve was not promptly refused: err=%v output=%s", err, out)
	}
	if _, err := getFixtureProjects(m); err != nil {
		t.Fatalf("original fixture is not still reachable: %v", err)
	}
}

func TestFixtureStaleLockAndMetadataAreHandledSafely(t *testing.T) {
	bin := fixtureBinary(t)
	staleReservation := filepath.Join(t.TempDir(), "released-lock")
	if err := os.Mkdir(staleReservation, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := openStateLock(staleReservation)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockStateFile(lock); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	startFixture(t, bin, staleReservation)

	staleMetadata := filepath.Join(t.TempDir(), "stale-metadata")
	if err := os.Mkdir(staleMetadata, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err = openStateLock(staleMetadata)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	privatePath := filepath.Join(staleMetadata, "private.json")
	original := []byte("stale fixture metadata must not be overwritten\n")
	if err := os.WriteFile(privatePath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := runBinary(t, bin, "start", "--state-dir", staleMetadata); err == nil {
		t.Fatalf("start reused state with stale metadata: %s", out)
	}
	got, err := os.ReadFile(privatePath)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("start overwrote metadata it did not own: %q err=%v", got, err)
	}
}

func TestFixtureConcurrentStartsHaveOneOwner(t *testing.T) {
	bin := fixtureBinary(t)
	for i := 0; i < 8; i++ {
		dir := filepath.Join(t.TempDir(), "racing-starts")
		start := make(chan struct{})
		var wg sync.WaitGroup
		outs := make([]string, 2)
		errs := make([]error, 2)
		for n := range outs {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				<-start
				outs[n], errs[n] = runBinary(t, bin, "start", "--state-dir", dir)
			}(n)
		}
		close(start)
		wg.Wait()
		winner := -1
		for n, err := range errs {
			if err == nil {
				if winner >= 0 {
					t.Fatalf("racing start commands both claimed one state directory: %q / %q", outs[winner], outs[n])
				}
				winner = n
			}
		}
		if winner < 0 {
			t.Fatalf("racing start commands had no owner: %v %v", errs, outs)
		}
		if out, err := runBinary(t, bin, "stop", "--state-dir", dir); err != nil {
			t.Fatalf("stop racing winner: %v %s", err, out)
		}
	}
}

func TestFixtureHostAndClientRequestsHaveOverallTimeout(t *testing.T) {
	bin := fixtureBinary(t)
	cases := []struct {
		name, command, response string
	}{
		{"client-headers", "client", ""},
		{"client-body", "client", "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nx"},
		{"stop-headers", "stop", ""},
	}
	for _, test := range cases {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var held []net.Conn
		accepted := make(chan struct{}, 1)
		var cleanupOnce sync.Once
		done := make(chan struct{})
		serveDone := make(chan struct{})
		go func() {
			defer close(serveDone)
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				mu.Lock()
				held = append(held, conn)
				mu.Unlock()
				select {
				case accepted <- struct{}{}:
				default:
				}
				if test.response != "" {
					_, _ = conn.Write([]byte(test.response))
				}
				select {
				case <-done:
					return
				default:
				}
			}
		}()
		cleanup := func() {
			cleanupOnce.Do(func() {
				close(done)
				_ = ln.Close()
				mu.Lock()
				for _, conn := range held {
					_ = conn.Close()
				}
				mu.Unlock()
				select {
				case <-serveDone:
				case <-time.After(time.Second):
					t.Errorf("%s test listener did not stop accepting connections", test.name)
				}
			})
		}
		t.Cleanup(cleanup)
		dir := filepath.Join(t.TempDir(), test.name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		meta := fixtureMeta{Endpoint: "http://" + ln.Addr().String(), RedirectEndpoint: "http://" + ln.Addr().String(), Identity: fixtureIdentity, ClientToken: "synthetic-client", AdminToken: "synthetic-admin"}
		if err := writeJSON(filepath.Join(dir, "private.json"), meta, 0600); err != nil {
			t.Fatal(err)
		}
		args := []string{test.command, "--state-dir", dir}
		if test.command == "client" {
			args = append(args, "list")
		}
		began := time.Now()
		// The request itself has a 2s HTTP deadline. Keep a separate 8s process
		// watchdog so race/scheduler delays cannot masquerade as that deadline.
		// A successful stop response could additionally spend up to 2s polling
		// listener shutdown, but this server withholds headers so that phase is
		// never entered here.
		out, err := runBinaryTimeout(t, 8*time.Second, bin, nil, args...)
		elapsed := time.Since(began)
		cleanup()
		select {
		case <-accepted:
		default:
			t.Errorf("%s request failed without the fixture listener accepting an HTTP connection: elapsed=%s err=%v output=%s", test.name, elapsed, err, out)
			continue
		}
		if err == nil || err == context.DeadlineExceeded {
			t.Errorf("%s request did not return before the process watchdog: elapsed=%s err=%v output=%s", test.name, elapsed, err, out)
			continue
		}
		lowerOutput := strings.ToLower(out)
		if !strings.Contains(lowerOutput, "context deadline exceeded") {
			t.Errorf("%s request failed without reporting its HTTP deadline cause: elapsed=%s err=%v output=%s", test.name, elapsed, err, out)
		}
	}
}

func TestFixtureRequestTimeoutIsWiredToHostAndClientRequests(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !fixtureRequestTimeoutIsTwoSeconds(file) {
		t.Fatal("fixtureRequestTimeout must remain exactly 2*time.Second")
	}

	functions := make(map[string]*ast.FuncDecl)
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			functions[fn.Name.Name] = fn
		}
	}
	for _, name := range []string{"admin", "client"} {
		fn := functions[name]
		if fn == nil || !functionPassesFixtureRequestTimeout(fn) {
			t.Errorf("%s must call doFixtureRequest(req, fixtureRequestTimeout)", name)
		}
	}
	fn := functions["doFixtureRequest"]
	if fn == nil || !fixtureRequestUsesTimeout(fn) {
		t.Fatal("doFixtureRequest must apply its timeout to both request context and HTTP client")
	}
}

func fixtureRequestTimeoutIsTwoSeconds(file *ast.File) bool {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range values.Names {
				if name.Name == "fixtureRequestTimeout" && i < len(values.Values) {
					return isTwoSeconds(values.Values[i])
				}
			}
		}
	}
	return false
}

func isTwoSeconds(expr ast.Expr) bool {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary.Op != token.MUL {
		return false
	}
	seconds, ok := binary.X.(*ast.BasicLit)
	if !ok || seconds.Kind != token.INT || seconds.Value != "2" {
		return false
	}
	unit, ok := binary.Y.(*ast.SelectorExpr)
	return ok && isIdent(unit.X, "time") && unit.Sel.Name == "Second"
}

func functionPassesFixtureRequestTimeout(fn *ast.FuncDecl) bool {
	calls, wiredCalls := 0, 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			if isIdent(n.Fun, "doFixtureRequest") {
				calls++
			}
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE || len(n.Lhs) != 2 || len(n.Rhs) != 1 ||
				!isIdent(n.Lhs[0], "resp") || !isIdent(n.Lhs[1], "e") {
				break
			}
			call, ok := n.Rhs[0].(*ast.CallExpr)
			if ok && isIdent(call.Fun, "doFixtureRequest") && len(call.Args) == 2 &&
				isIdent(call.Args[0], "req") && isIdent(call.Args[1], "fixtureRequestTimeout") {
				wiredCalls++
			}
		}
		return true
	})
	return calls == 1 && wiredCalls == 1
}

func fixtureRequestUsesTimeout(fn *ast.FuncDecl) bool {
	contextDeadline, clientTimeout := false, false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			selector, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || !isIdent(selector.X, "context") || selector.Sel.Name != "WithTimeout" || len(n.Args) != 2 || !isIdent(n.Args[1], "timeout") {
				break
			}
			requestContext, ok := n.Args[0].(*ast.CallExpr)
			if ok && len(requestContext.Args) == 0 {
				requestSelector, ok := requestContext.Fun.(*ast.SelectorExpr)
				contextDeadline = ok && isIdent(requestSelector.X, "req") && requestSelector.Sel.Name == "Context"
			}
		case *ast.CompositeLit:
			selector, ok := n.Type.(*ast.SelectorExpr)
			if !ok || !isIdent(selector.X, "http") || selector.Sel.Name != "Client" {
				break
			}
			for _, elt := range n.Elts {
				field, ok := elt.(*ast.KeyValueExpr)
				if ok && isIdent(field.Key, "Timeout") && isIdent(field.Value, "timeout") {
					clientTimeout = true
				}
			}
		}
		return true
	})
	return contextDeadline && clientTimeout
}

func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func TestFixtureStartWaitsForAuthenticatedListener(t *testing.T) {
	bin := fixtureBinary(t)
	dir := filepath.Join(t.TempDir(), "delayed")
	began := time.Now()
	out, err := runBinaryTimeout(t, 5*time.Second, bin, []string{"BROKER_FIXTURE_TEST_STARTUP_DELAY=300ms"}, "start", "--state-dir", dir)
	if err != nil {
		t.Fatalf("start after listener becomes ready: %v %s", err, out)
	}
	m, err := readMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopFixtureEndpoint(t, m)
		if err := waitListenerStopped(m.Endpoint); err != nil {
			t.Errorf("delayed fixture cleanup: %v", err)
		}
	})
	if elapsed := time.Since(began); elapsed < 250*time.Millisecond {
		t.Fatalf("start reported success from metadata before delayed authenticated listener was ready: %s", elapsed)
	}
	if _, err := getFixtureProjects(m); err != nil {
		t.Fatalf("reported-ready fixture did not authenticate: %v", err)
	}
}

func TestSignedFixtureExpectationIsPendingUntilGH(t *testing.T) {
	path := filepath.Join("..", "..", "integration", "testdata", "pack-broker-http", "loopholes", "fixture-signed-call", "executor-input.v1.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ExecutorResult struct {
			Status  json.RawMessage `json:"status"`
			Pending string          `json:"pending"`
		} `json:"executor_result"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if string(fixture.ExecutorResult.Status) != `"pending"` || fixture.ExecutorResult.Pending == "" {
		t.Fatalf("signed service result claims an unimplemented G/H success: %+v", fixture.ExecutorResult)
	}
}

func fixtureListenerAccepts(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("tcp", u.Host, 50*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func getFixtureProjects(m fixtureMeta) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Endpoint+"/cli/projects", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.ClientToken)
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func stopFixtureEndpoint(t *testing.T, m fixtureMeta) {
	t.Helper()
	if err := requestStopFixture(m); err != nil {
		t.Fatalf("stop original fixture endpoint: %v", err)
	}
}

func requestStopFixture(m fixtureMeta) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint+"/_host/stop", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Fixture-Admin", m.AdminToken)
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected stop status %s", resp.Status)
	}
	return nil
}
