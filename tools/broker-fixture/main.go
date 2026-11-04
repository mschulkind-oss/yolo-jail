// broker-fixture is a host-operated synthetic CLI/HTTP service for pack-broker tests.
// It intentionally lives outside the production Go source bundle.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	fixtureIdentity       = "synthetic-pack-broker-fixture-v1"
	fixtureRequestTimeout = 2 * time.Second
)

type fixtureItem struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	State   string `json:"state"`
}
type fixtureWrite struct {
	Operation string `json:"operation"`
	Item      string `json:"item"`
	State     string `json:"state"`
}
type fixtureState struct {
	Projects []string       `json:"projects"`
	Items    []fixtureItem  `json:"items"`
	Writes   []fixtureWrite `json:"writes"`
}
type fixtureMeta struct {
	Endpoint         string `json:"endpoint"`
	RedirectEndpoint string `json:"redirect_endpoint,omitempty"`
	Identity         string `json:"identity"`
	ClientToken      string `json:"synthetic_client_token"`
	AdminToken       string `json:"synthetic_host_admin_token"`
	Scenario         string `json:"scenario,omitempty"`
}
type fixtureService struct {
	dir      string
	mu       sync.Mutex
	state    fixtureState
	meta     fixtureMeta
	shutdown context.CancelFunc
	stopping bool
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := serve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: broker-fixture start|seed|inspect|change|scenario|reset|stop|client")
	}
	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	dir := fs.String("state-dir", "", "private fixture state directory (host controls only)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	tail := fs.Args()
	switch command {
	case "start":
		if len(tail) != 0 {
			return errors.New("usage: broker-fixture start [--state-dir DIR]")
		}
		return start(*dir)
	case "seed", "inspect", "reset", "stop":
		if *dir == "" || len(tail) != 0 {
			return errors.New("use --state-dir with the path printed by start")
		}
		return admin(*dir, command, nil)
	case "change":
		if *dir == "" || len(tail) != 2 {
			return errors.New("usage: broker-fixture change --state-dir DIR ITEM STATE")
		}
		return admin(*dir, command, tail)
	case "scenario":
		if *dir == "" || len(tail) != 1 {
			return errors.New("usage: broker-fixture scenario --state-dir DIR lookup-failure|redirect|credential-echo|oversize|none")
		}
		return admin(*dir, command, tail)
	case "client":
		if *dir == "" || len(tail) == 0 {
			return errors.New("usage: broker-fixture client --state-dir DIR list|get ITEM|update ITEM STATE")
		}
		return client(*dir, tail)
	default:
		return errors.New("unknown fixture command")
	}
}
func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func writeJSON(path string, v any, mode os.FileMode) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".fixture-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(append(b, '\n'))
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func readMeta(dir string) (fixtureMeta, error) {
	var m fixtureMeta
	b, e := os.ReadFile(filepath.Join(dir, "private.json"))
	if e != nil {
		return m, e
	}
	e = json.Unmarshal(b, &m)
	if e != nil {
		return m, e
	}
	if m.Identity != fixtureIdentity || m.ClientToken == "" || m.AdminToken == "" {
		return m, errors.New("invalid synthetic fixture metadata")
	}
	if m.Endpoint != "" {
		if !pinnedLoopbackURL(m.Endpoint) {
			return m, errors.New("fixture endpoint is not the pinned loopback listener")
		}
		if !pinnedLoopbackURL(m.RedirectEndpoint) {
			return m, errors.New("fixture redirect listener is not pinned to loopback")
		}
	}
	return m, nil
}
func pinnedLoopbackURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
func start(dir string) error {
	if dir == "" {
		var e error
		dir, e = os.MkdirTemp("", "yolo-pack-broker-fixture-")
		if e != nil {
			return e
		}
	} else {
		abs, e := filepath.Abs(dir)
		if e != nil {
			return e
		}
		dir = abs
		if e = os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		info, e := os.Lstat(dir)
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("fixture state path must be a real directory; choose a private directory")
		}
		if e = os.Chmod(dir, 0700); e != nil {
			return e
		}
	}
	lock, e := openStateLock(dir)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = lockStateFile(lock); e != nil {
		return e
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Name() != stateLockName {
			return errors.New("fixture state directory is not empty; choose a new private directory")
		}
	}
	clientKey, e := randomSecret()
	if e != nil {
		return e
	}
	adminKey, e := randomSecret()
	if e != nil {
		return e
	}
	signingKey, e := randomSecret()
	if e != nil {
		return e
	}
	m := fixtureMeta{Identity: fixtureIdentity, ClientToken: clientKey, AdminToken: adminKey}
	if e = writeJSON(filepath.Join(dir, "private.json"), m, 0600); e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(dir, "synthetic-signing-key"), []byte(signingKey), 0600); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(dir, "state.json"), fixtureState{Projects: []string{}, Items: []fixtureItem{}, Writes: []fixtureWrite{}}, 0600); e != nil {
		return e
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	child := exec.Command(exe, "serve", "--state-dir", dir, "--lock-fd", "3")
	child.ExtraFiles = []*os.File{lock}
	log, e := os.OpenFile(filepath.Join(dir, "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	child.Stdout, child.Stderr = log, log
	if e = child.Start(); e != nil {
		log.Close()
		return e
	}
	_ = log.Close()
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-waited:
			return fmt.Errorf("fixture listener exited before readiness: %v; inspect private service log: %s", err, filepath.Join(dir, "service.log"))
		default:
		}
		m, e = readMeta(dir)
		if e == nil && m.Endpoint != "" && fixtureListenerReady(m, 200*time.Millisecond) {
			fmt.Printf("endpoint: %s\nidentity: %s (synthetic)\nstate: %s\n", m.Endpoint, m.Identity, dir)
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = child.Process.Kill()
	<-waited
	return fmt.Errorf("fixture listener did not become ready; inspect private service log: %s", filepath.Join(dir, "service.log"))
}
func fixtureListenerReady(m fixtureMeta, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Endpoint+"/_host/ready", nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-Fixture-Admin", m.AdminToken)
	resp, err := doFixtureRequest(req, timeout)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

// A forced deadline is process-fatal so deferred cleanup cannot release state.lock under a live writer.
func serve(args []string) error {
	return serveWithRedirectBeforeWriteAndFatal(args, nil, func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	})
}

// serveWithRedirectBeforeWrite keeps the redirect write boundary controllable for teardown tests.
func serveWithRedirectBeforeWrite(args []string, beforeWrite func()) error {
	return serveWithRedirectBeforeWriteAndFatal(args, beforeWrite, nil)
}

func serveWithRedirectBeforeWriteAndFatal(args []string, beforeWrite func(), fatalShutdown func(error)) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dir := fs.String("state-dir", "", "")
	lockFD := fs.Int("lock-fd", -1, "")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if *dir == "" {
		return errors.New("missing fixture state directory")
	}
	abs, e := filepath.Abs(*dir)
	if e != nil {
		return e
	}
	*dir = abs
	var lock *os.File
	if *lockFD >= 0 {
		lock = os.NewFile(uintptr(*lockFD), filepath.Join(*dir, stateLockName))
		if e = validateInheritedStateLock(*dir, lock); e != nil {
			return e
		}
	} else {
		lock, e = openStateLock(*dir)
		if e != nil {
			return e
		}
		if e = lockStateFile(lock); e != nil {
			lock.Close()
			return e
		}
	}
	defer lock.Close()
	m, e := readMeta(*dir)
	if e != nil {
		return e
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	redir, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		ln.Close()
		return e
	}
	m.Endpoint = "http://" + ln.Addr().String()
	m.RedirectEndpoint = "http://" + redir.Addr().String()
	if e = writeJSON(filepath.Join(*dir, "private.json"), m, 0600); e != nil {
		ln.Close()
		redir.Close()
		return e
	}
	s := &fixtureService{dir: *dir, meta: m}
	b, _ := os.ReadFile(filepath.Join(*dir, "state.json"))
	if e = json.Unmarshal(b, &s.state); e != nil {
		ln.Close()
		redir.Close()
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.shutdown = cancel
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", s.graphql)
	mux.HandleFunc("/cli/", s.cliAPI)
	mux.HandleFunc("/_host/ready", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !s.authorized(r, true) {
			http.Error(w, "authenticated readiness probe required", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/_host/", s.hostAPI)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, MaxHeaderBytes: 16 << 10}
	redirectSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if beforeWrite != nil {
			beforeWrite()
		}
		s.mu.Lock()
		if !s.stopping {
			if holdPath := os.Getenv("BROKER_FIXTURE_TEST_HOLD_REDIRECT_WRITE"); holdPath != "" {
				if err := os.WriteFile(holdPath+".entered", []byte("held"), 0600); err == nil {
					for {
						if _, err := os.Stat(holdPath + ".release"); err == nil {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
			}
			old := fixtureState{Projects: append([]string(nil), s.state.Projects...), Items: append([]fixtureItem(nil), s.state.Items...), Writes: append([]fixtureWrite(nil), s.state.Writes...)}
			s.state.Writes = append(s.state.Writes, fixtureWrite{Operation: "redirect-listener", Item: r.URL.Path})
			if err := s.saveLocked(); err != nil {
				s.state = old
				s.mu.Unlock()
				http.Error(w, "cannot persist fixture state", http.StatusInternalServerError)
				return
			}
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}), ReadHeaderTimeout: 2 * time.Second}
	shutdownDone := make(chan error, 1)
	redirectServeDone := make(chan error, 1)
	go func() { redirectServeDone <- redirectSrv.Serve(redir) }()
	go func() {
		<-ctx.Done()
		c, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		var shutdownErrs []error
		if err := srv.Shutdown(c); err != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("shut down primary fixture listener: %w", err))
			_ = srv.Close()
			_ = redirectSrv.Close()
			shutdownDone <- errors.Join(shutdownErrs...)
			return
		}
		if err := redirectSrv.Shutdown(c); err != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("shut down redirect fixture listener: %w", err))
			_ = srv.Close()
			_ = redirectSrv.Close()
			shutdownDone <- errors.Join(shutdownErrs...)
			return
		}
		if err := <-redirectServeDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("serve redirect fixture listener: %w", err))
		}
		s.mu.Lock()
		s.stopping = true
		s.mu.Unlock()
		shutdownDone <- errors.Join(shutdownErrs...)
	}()
	if raw := os.Getenv("BROKER_FIXTURE_TEST_STARTUP_DELAY"); raw != "" {
		if delay, err := time.ParseDuration(raw); err == nil && delay > 0 && delay <= 2*time.Second {
			time.Sleep(delay)
		}
	}
	e = srv.Serve(ln)
	cancel()
	shutdownErr := <-shutdownDone
	if shutdownErr != nil && fatalShutdown != nil {
		fatalShutdown(shutdownErr)
		return shutdownErr
	}
	if errors.Is(e, http.ErrServerClosed) {
		return shutdownErr
	}
	return errors.Join(e, shutdownErr)
}

type cancelResponseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (b *cancelResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.cancel)
	return err
}

func doFixtureRequest(req *http.Request, timeout time.Duration) (*http.Response, error) {
	if timeout <= 0 {
		timeout = fixtureRequestTimeout
	}
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	resp, err := (&http.Client{Timeout: timeout}).Do(req.Clone(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelResponseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

func (s *fixtureService) saveLocked() error {
	if s.stopping {
		return errors.New("fixture listener shutdown did not drain; state writes are closed")
	}
	return writeJSON(filepath.Join(s.dir, "state.json"), s.state, 0600)
}
func (s *fixtureService) scenario() string { s.mu.Lock(); defer s.mu.Unlock(); return s.meta.Scenario }
func (s *fixtureService) authorized(r *http.Request, admin bool) bool {
	want := s.meta.ClientToken
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if admin {
		want = s.meta.AdminToken
		got = r.Header.Get("X-Fixture-Admin")
	}
	return got == want && want != ""
}
func (s *fixtureService) hostAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorized(r, true) {
		http.Error(w, "host control authentication required", http.StatusForbidden)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	if parts[1] == "stop" {
		w.WriteHeader(http.StatusNoContent)
		go s.shutdown()
		return
	}
	var in struct {
		Item     string `json:"item"`
		State    string `json:"state"`
		Scenario string `json:"scenario"`
	}
	if parts[1] == "change" || parts[1] == "scenario" {
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); e != nil {
			http.Error(w, "invalid control request", 400)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch parts[1] {
	case "seed":
		s.state = fixtureState{Projects: []string{"alpha", "beta"}, Items: []fixtureItem{{"a1", "alpha", "open"}, {"b1", "beta", "open"}}}
	case "inspect":
	case "change":
		if e := s.change(in.Item, in.State, "host-change"); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
	case "reset":
		s.state = fixtureState{Projects: []string{}, Items: []fixtureItem{}, Writes: []fixtureWrite{}}
	case "scenario":
		switch in.Scenario {
		case "none", "lookup-failure", "redirect", "credential-echo", "oversize":
			s.meta.Scenario = in.Scenario
			if in.Scenario == "none" {
				s.meta.Scenario = ""
			}
			if e := writeJSON(filepath.Join(s.dir, "private.json"), s.meta, 0600); e != nil {
				http.Error(w, "cannot persist synthetic scenario", 500)
				return
			}
		default:
			http.Error(w, "unknown synthetic scenario", 400)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	if e := s.saveLocked(); e != nil {
		http.Error(w, "cannot persist fixture state", 500)
		return
	}
	if parts[1] == "inspect" {
		jsonResponse(w, s.state)
	} else {
		w.WriteHeader(204)
	}
}
func (s *fixtureService) change(id, state, op string) error {
	if (id != "a1" && id != "b1") || state == "" || len(state) > 40 {
		return errors.New("unknown item or invalid state; seed the synthetic fixture and use a short state")
	}
	for i := range s.state.Items {
		if s.state.Items[i].ID == id {
			s.state.Items[i].State = state
			s.state.Writes = append(s.state.Writes, fixtureWrite{op, id, state})
			return nil
		}
	}
	return errors.New("fixture item is not seeded; run seed")
}
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (s *fixtureService) cliAPI(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r, false) {
		http.Error(w, "synthetic fixture credential required", http.StatusForbidden)
		return
	}
	if s.scenario() == "lookup-failure" {
		http.Error(w, "synthetic lookup failure", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/cli/projects" {
		s.mu.Lock()
		defer s.mu.Unlock()
		jsonResponse(w, s.state.Projects)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/cli/items/") {
		id := strings.TrimPrefix(r.URL.Path, "/cli/items/")
		if r.Method == http.MethodGet {
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, it := range s.state.Items {
				if it.ID == id {
					jsonResponse(w, it)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			var in struct {
				State string `json:"state"`
			}
			if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); e != nil {
				http.Error(w, "invalid client request", 400)
				return
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			old := fixtureState{Projects: append([]string(nil), s.state.Projects...), Items: append([]fixtureItem(nil), s.state.Items...), Writes: append([]fixtureWrite(nil), s.state.Writes...)}
			if e := s.change(id, in.State, "cli-client"); e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			if e := s.saveLocked(); e != nil {
				s.state = old
				http.Error(w, "cannot persist fixture state", http.StatusInternalServerError)
				return
			}
			jsonResponse(w, s.state.Items)
			return
		}
	}
	http.NotFound(w, r)
}
func decodeGraphQLJSONRequest(body []byte) (query, operationName string, isJSON bool, err error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return "", "", false, nil
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, tokenErr := decoder.Token()
		if tokenErr != nil {
			if len(fields) == 0 {
				return "", "", false, nil
			}
			return "", "", true, errors.New("invalid GraphQL JSON request")
		}
		key, ok := token.(string)
		if !ok || (key != "query" && key != "operationName") {
			return "", "", true, errors.New("GraphQL JSON request accepts only query and operationName")
		}
		if _, exists := fields[key]; exists {
			return "", "", true, errors.New("duplicate GraphQL JSON field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "", "", true, errors.New("invalid GraphQL JSON request")
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return "", "", true, errors.New("invalid GraphQL JSON request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", "", true, errors.New("invalid GraphQL JSON request")
	}
	queryRaw, exists := fields["query"]
	if !exists || json.Unmarshal(queryRaw, &query) != nil || query == "" {
		return "", "", true, errors.New("GraphQL JSON request requires a non-empty query")
	}
	if raw, exists := fields["operationName"]; exists && json.Unmarshal(raw, &operationName) != nil {
		return "", "", true, errors.New("GraphQL JSON operationName must be a string")
	}
	return query, operationName, true, nil
}

func (s *fixtureService) graphql(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorized(r, false) {
		http.Error(w, "synthetic fixture credential required", http.StatusForbidden)
		return
	}
	values, queryErr := url.ParseQuery(r.URL.RawQuery)
	if queryErr != nil {
		http.Error(w, "invalid GraphQL URL query", 400)
		return
	}
	for key := range values {
		if key != "operationName" || len(values[key]) != 1 {
			http.Error(w, "only operationName is accepted in the URL query", 400)
			return
		}
	}
	urlOperationName := values.Get("operationName")
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if e != nil {
		http.Error(w, "GraphQL fixture body exceeds 1 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	operationName := urlOperationName
	trim := bytes.TrimSpace(body)
	if len(trim) > 0 && trim[0] == '{' {
		query, bodyOperationName, isJSON, decodeErr := decodeGraphQLJSONRequest(trim)
		if decodeErr != nil {
			http.Error(w, decodeErr.Error(), 400)
			return
		}
		if isJSON {
			if urlOperationName != "" && bodyOperationName != "" && urlOperationName != bodyOperationName {
				http.Error(w, "conflicting GraphQL operationName selectors", http.StatusBadRequest)
				return
			}
			body = []byte(query)
			if bodyOperationName != "" {
				operationName = bodyOperationName
			}
		}
	}
	switch s.scenario() {
	case "redirect":
		http.Redirect(w, r, s.meta.RedirectEndpoint+"/sink", http.StatusFound)
		return
	case "credential-echo":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":{"echo":%q}}`, r.Header.Get("Authorization"))
		return
	case "oversize":
		w.WriteHeader(200)
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for i := 0; i < 513; i++ {
			if _, e = w.Write(chunk); e != nil {
				return
			}
		}
		return
	case "lookup-failure":
		http.Error(w, "synthetic lookup failure", http.StatusServiceUnavailable)
		return
	}
	op, e := parseGraphQL(body, operationName)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range op.fields {
		if f.name == "updateWorkItem" && (f.args["id"] != "a1" && f.args["id"] != "b1" || f.args["state"] == "") {
			http.Error(w, "unknown item or missing state", 400)
			return
		}
	}
	if op.kind == "mutation" {
		old := s.state
		next := fixtureState{Projects: append([]string(nil), old.Projects...), Items: append([]fixtureItem(nil), old.Items...), Writes: append([]fixtureWrite(nil), old.Writes...)}
		s.state = next
		for _, f := range op.fields {
			if f.name == "updateWorkItem" {
				if e = s.change(f.args["id"], f.args["state"], "graphql:"+op.name); e != nil {
					s.state = old
					http.Error(w, e.Error(), 400)
					return
				}
			}
		}
		if e = s.saveLocked(); e != nil {
			s.state = old
			http.Error(w, "cannot persist fixture state", 500)
			return
		}
	}
	jsonResponse(w, map[string]any{"data": s.graphqlData(op)})
}
func (s *fixtureService) graphqlData(op gqlOperation) map[string]any {
	data := map[string]any{}
	for _, f := range op.fields {
		switch f.name {
		case "project":
			p := map[string]any{}
			for _, sub := range f.children {
				if sub.name == "name" {
					p[sub.alias] = f.args["id"]
				}
				if sub.name == "items" {
					items := []any{}
					for _, it := range s.state.Items {
						if it.Project == f.args["id"] {
							items = append(items, projectItem(it, sub.children))
						}
					}
					p[sub.alias] = items
				}
			}
			data[f.alias] = p
		case "item", "updateWorkItem":
			for _, it := range s.state.Items {
				if it.ID == f.args["id"] {
					data[f.alias] = projectItem(it, f.children)
				}
			}
		}
	}
	return data
}
func projectItem(it fixtureItem, fields []gqlField) map[string]any {
	o := map[string]any{}
	for _, f := range fields {
		if f.name == "id" {
			o[f.alias] = it.ID
		}
		if f.name == "state" {
			o[f.alias] = it.State
		}
	}
	return o
}
func admin(dir, command string, args []string) error {
	m, e := readMeta(dir)
	if e != nil {
		return e
	}
	var data any
	if command == "change" {
		data = map[string]string{"item": args[0], "state": args[1]}
	}
	if command == "scenario" {
		data = map[string]string{"scenario": args[0]}
	}
	var body io.Reader
	if data != nil {
		b, _ := json.Marshal(data)
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequest("POST", m.Endpoint+"/_host/"+command, body)
	if e != nil {
		return e
	}
	req.Header.Set("X-Fixture-Admin", m.AdminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, e := doFixtureRequest(req, fixtureRequestTimeout)
	if e != nil {
		return fmt.Errorf("fixture host control failed: %w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("fixture host control %s: %s", command, strings.TrimSpace(string(b)))
	}
	if command == "inspect" {
		_, e = io.Copy(os.Stdout, resp.Body)
		return e
	}
	if command == "stop" {
		if e := waitListenerStopped(m.Endpoint); e != nil {
			return e
		}
		fmt.Println("stopped synthetic fixture listener")
	}
	return nil
}
func waitListenerStopped(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", u.Host, 50*time.Millisecond)
		if dialErr != nil {
			return nil
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("fixture listener did not stop; inspect its private service log")
}
func client(dir string, args []string) error {
	m, e := readMeta(dir)
	if e != nil {
		return e
	}
	var method, path string
	var body io.Reader
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: client list")
		}
		method, path = "GET", m.Endpoint+"/cli/projects"
	case "get":
		if len(args) != 2 {
			return errors.New("usage: client get ITEM")
		}
		method, path = "GET", m.Endpoint+"/cli/items/"+args[1]
	case "update":
		if len(args) != 3 {
			return errors.New("usage: client update ITEM STATE")
		}
		method, path = "POST", m.Endpoint+"/cli/items/"+args[1]
		body = strings.NewReader(`{"state":` + strconv.Quote(args[2]) + `}`)
	default:
		return errors.New("unsupported fixture CLI command")
	}
	req, e := http.NewRequest(method, path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+m.ClientToken)
	req.Header.Set("Content-Type", "application/json")
	resp, e := doFixtureRequest(req, fixtureRequestTimeout)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("fixture CLI returned %s: %s", resp.Status, string(b))
	}
	_, e = io.Copy(os.Stdout, resp.Body)
	return e
}
