package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	fs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"testing/fstest"

	"github.com/mschulkind-oss/yolo-jail/internal/releasematrix"
)

const fakeHostGo = `#!/bin/sh
printf '%s\n' "$*" >> "$EMITTER_LOG"
case "$1" in
  env)
    case "$EMITTER_PROXY_MODE" in
      fail) printf 'synthetic proxy failure\n' >&2; exit 19 ;;
      malformed) printf '{not-json}\n' ;;
      overflow) printf '%70000s' '' ;;
      stderr-overflow) printf '%17000s' '' >&2 ;;
      wait) exec /bin/cat "$EMITTER_GATE" ;;
      off) printf '{"GOPROXY":"off","GOSUMDB":"off"}\n' ;;
      *) printf '{"GOPROXY":"https://proxy.synthetic.invalid","GOSUMDB":"sum.golang.org"}\n' ;;
    esac
    ;;
  mod)
    case "$EMITTER_DOWNLOAD_MODE" in
      fail) printf '{"Error":"synthetic module refusal"}\n'; exit 7 ;;
      malformed) printf '{not-json}\n' ;;
      overflow) printf '%1100000s' '' ;;
      stderr-overflow) printf '%17000s' '' >&2 ;;
      *) printf '{"Dir":"%s","Sum":"h1:synthetic"}\n' "$EMITTER_TC" ;;
    esac
    ;;
  *) exit 21 ;;
esac
`

const fakeToolchainGo = `#!/bin/sh
printf '%s\n' "$*" >> "$EMITTER_LOG"
case "$1" in
  version)
    case "$EMITTER_VERSION_MODE" in
      overflow) printf '%5000s' '' ;;
      fail) printf 'synthetic version failure\n' >&2; exit 31 ;;
      wrong) printf 'go version go1.0 synthetic\n' ;;
      *) printf 'go version go1.26.7 %s/%s\n' "$EMITTER_GOOS" "$EMITTER_GOARCH" ;;
    esac
    ;;
  build)
    printf 'build-started\n' >> "$EMITTER_MARKER"
    output=
    previous=
    for arg in "$@"; do
      if [ "$previous" = -o ]; then output=$arg; fi
      previous=$arg
    done
    [ -n "$output" ] || exit 23
    printf 'synthetic binary bytes\n' > "$output"
    ;;
  *) exit 22 ;;
esac
`

type testWireFrame struct {
	Protocol int             `json:"protocol"`
	Nonce    string          `json:"nonce"`
	Sequence uint64          `json:"sequence"`
	Kind     string          `json:"kind"`
	Payload  json.RawMessage `json:"payload"`
}

type fakeFetch struct {
	hostGo  string
	goBin   string
	temp    string
	environ []string
	log     string
	marker  string
	gate    string
}

func newFakeFetch(t *testing.T, proxyMode, downloadMode string, nonExecutable bool) fakeFetch {
	t.Helper()
	root := t.TempDir()
	binDir, tcDir := filepath.Join(root, "commands"), filepath.Join(root, "toolchain")
	if err := os.MkdirAll(filepath.Join(tcDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath, marker := filepath.Join(root, "commands.log"), filepath.Join(root, "build.marker")
	gate := filepath.Join(root, "wait.gate")
	if proxyMode == "wait" {
		if err := syscall.Mkfifo(gate, 0o600); err != nil {
			t.Fatal("cannot create synthetic command gate")
		}
	}
	host, goBin := filepath.Join(binDir, "go"), filepath.Join(tcDir, "bin", "go")
	if err := os.WriteFile(host, []byte(fakeHostGo), 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o755)
	if nonExecutable {
		mode = 0o644
	}
	if err := os.WriteFile(goBin, []byte(fakeToolchainGo), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tcDir, "bin", "gofmt"), []byte("synthetic gofmt"), 0o644); err != nil {
		t.Fatal(err)
	}
	goos, goarch := runtime.GOOS, runtime.GOARCH
	environ := []string{
		"PATH=" + binDir,
		"HOME=" + filepath.Join(root, "home"),
		"GOENV=" + filepath.Join(root, "go.env"),
		"GOCACHE=" + filepath.Join(root, "build-cache"),
		"GOMODCACHE=" + filepath.Join(root, "module-cache"),
		"GOWORK=" + filepath.Join(root, "go.work"),
		"GOFLAGS=-mod=vendor",
		"GOPROXY=https://fallback.synthetic.invalid",
		"GOSUMDB=off",
		"EMITTER_LOG=" + logPath,
		"EMITTER_TC=" + tcDir,
		"EMITTER_MARKER=" + marker,
		"EMITTER_GATE=" + gate,
		"EMITTER_PROXY_MODE=" + proxyMode,
		"EMITTER_DOWNLOAD_MODE=" + downloadMode,
		"EMITTER_VERSION_MODE=ok",
		"EMITTER_GOOS=" + goos,
		"EMITTER_GOARCH=" + goarch,
	}
	return fakeFetch{hostGo: host, goBin: goBin, temp: root, environ: environ, log: logPath, marker: marker, gate: gate}
}

func writeTestFrame(w io.Writer, nonce string, seq uint64, kind string, payload any) error {
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data, err := json.Marshal(testWireFrame{Protocol: toolchainProtocolVersion, Nonce: nonce, Sequence: seq, Kind: kind, Payload: p})
	if err != nil {
		return err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readTestFrame(r io.Reader) (testWireFrame, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return testWireFrame{}, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxProtocolFrame {
		return testWireFrame{}, fmt.Errorf("test frame length %d out of range", n)
	}
	data := make([]byte, int(n))
	if _, err := io.ReadFull(r, data); err != nil {
		return testWireFrame{}, err
	}
	var frame testWireFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return testWireFrame{}, err
	}
	return frame, nil
}

func readTestFrameBounded(t *testing.T, r *io.PipeReader) testWireFrame {
	t.Helper()
	type result struct {
		frame testWireFrame
		err   error
	}
	read := make(chan result, 1)
	go func() {
		frame, err := readTestFrame(r)
		read <- result{frame: frame, err: err}
	}()
	select {
	case got := <-read:
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got.frame
	case <-time.After(5 * time.Second):
		_ = r.Close()
		t.Fatal("timed out waiting for private protocol frame")
		return testWireFrame{}
	}
}

type signalReadCloser struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (r *signalReadCloser) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.ReadCloser.Read(p)
}

func newFetchSession(ctx context.Context) (*toolchainSession, *io.PipeReader, *io.PipeWriter, *io.PipeReader, *io.PipeWriter) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	parentRead, sessionWrite := io.Pipe()
	sessionRead, parentWrite := io.Pipe()
	s := &toolchainSession{input: &signalReadCloser{ReadCloser: sessionRead, started: make(chan struct{})}, output: sessionWrite,
		nonce: "synthetic-nonce-123", readSeq: 1, ctx: ctx, cancel: cancel, planSeen: true, wantCount: 1}
	return s, parentRead, parentWrite, sessionRead, sessionWrite
}

func decodePayload[T any](t *testing.T, frame testWireFrame) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(frame.Payload, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func fetchAndRelease(t *testing.T, fixture fakeFetch) (string, toolchainObservation) {
	t.Helper()
	s, parentRead, parentWrite, sessionRead, sessionWrite := newFetchSession(context.Background())
	defer s.cancel()
	defer parentRead.Close()
	defer parentWrite.Close()
	defer sessionRead.Close()
	defer sessionWrite.Close()
	type answer struct {
		path string
		err  error
	}
	result := make(chan answer, 1)
	go func() {
		path, err := fetchToolchainObserved(s.ctx, fixture.hostGo, fixture.environ, s)
		result <- answer{path, err}
	}()
	frame := readTestFrameBounded(t, parentRead)
	if frame.Kind != "RETURN" || frame.Sequence != 1 {
		t.Fatal("provider result frame was missing or out of sequence")
	}
	payload := decodePayload[struct {
		Outcome     string               `json:"outcome"`
		Observation toolchainObservation `json:"observation"`
	}](t, frame)
	if payload.Outcome != "provider_return" {
		t.Fatalf("return outcome = %q", payload.Outcome)
	}
	if _, err := os.Stat(fixture.marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("build marker appeared before RELEASE: %v", err)
	}
	input := s.input.(*signalReadCloser)
	select {
	case <-input.started:
	case <-time.After(5 * time.Second):
		t.Fatal("fetch never entered the controlled RELEASE read")
	}
	select {
	case <-result:
		t.Fatal("fetch returned before RELEASE was read")
	default:
	}
	if err := writeTestFrame(parentWrite, s.nonce, 2, "RELEASE", emptyPayload{}); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil {
		t.Fatal(got.err)
	}
	return got.path, payload.Observation
}

func TestGoEmitterObservedFetchUsesActualCommandBindingsAndContext(t *testing.T) {
	fixture := newFakeFetch(t, "custom", "ok", true)
	got, observed := fetchAndRelease(t, fixture)
	if got != fixture.goBin || observed.ReturnedGo != got || observed.Downloader != fixture.hostGo {
		t.Fatal("return was not bound to the actual chosen command")
	}
	if observed.Module != toolchainModule(runtime.GOOS, runtime.GOARCH) || observed.Toolchain != Toolchain || observed.ModuleSum != "h1:synthetic" {
		t.Fatal("runtime module or source pin was missing")
	}
	if observed.Proxy.Proxy != "https://proxy.synthetic.invalid" || observed.Proxy.Fallback != "none" || observed.Proxy.Command.Stage != "effective-proxy" {
		t.Fatal("effective proxy observation was missing")
	}
	if len(observed.Commands) != 2 || observed.Commands[0].Stage != "download" || observed.Commands[1].Stage != "version" {
		t.Fatal("actual download/version command order was not observed")
	}
	if !observed.Proxy.Command.Started || observed.Proxy.Command.Status != "exited" || observed.Proxy.Command.Args[1] != "env" {
		t.Fatal("actual effective-proxy command was not captured")
	}
	if observed.Proxy.Command.Environment["GOENV"].Value != strings.TrimPrefix(fixture.environ[2], "GOENV=") {
		t.Fatal("effective-proxy GOENV was not preserved")
	}
	if observed.Commands[0].Environment["GOENV"].Value != "off" || observed.Commands[0].Environment["GOPROXY"].Value != observed.Proxy.Proxy {
		t.Fatal("download environment transformation was not captured")
	}
	if observed.Commands[1].Environment["HOME"].Value != strings.TrimPrefix(fixture.environ[1], "HOME=") || observed.Commands[1].Environment["GOCACHE"].Value != strings.TrimPrefix(fixture.environ[3], "GOCACHE=") {
		t.Fatal("ordinary HOME/cache context was not preserved")
	}
	if observed.Commands[1].Before.Path != got || observed.Commands[1].After.Path != got || observed.Commands[1].ExitCode == nil || *observed.Commands[1].ExitCode != 0 {
		t.Fatal("chosen version command binding or status was missing")
	}
	if observed.Readiness.Directory != filepath.Dir(filepath.Dir(got)) || observed.Readiness.Status != "success" {
		t.Fatal("actual selected module readiness was not captured")
	}
}

func TestGoEmitterProxyOffAndFallbackPolicyAreObserved(t *testing.T) {
	for _, tc := range []struct{ name, mode, wantProxy, fallback string }{
		{"off", "off", "off", "none"},
		{"command failure", "fail", "https://fallback.synthetic.invalid", "command-failed"},
		{"invalid json", "malformed", "https://fallback.synthetic.invalid", "invalid-json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, observation := fetchAndRelease(t, newFakeFetch(t, tc.mode, "ok", false))
			if observation.Proxy.Fallback != tc.fallback || observation.Commands[0].Environment["GOPROXY"].Value != tc.wantProxy {
				t.Fatal("proxy fallback did not match the actual download environment")
			}
			if observation.Readiness.Status != "no-op" {
				t.Fatal("already-runnable selected toolchain was not observed as no-op")
			}
			if tc.fallback == "command-failed" && (observation.Proxy.Command.Status != "exit-failed" || observation.Proxy.Command.ExitCode == nil || *observation.Proxy.Command.ExitCode != 19) {
				t.Fatal("proxy command failure status was not observed")
			}
		})
	}
}

func TestGoEmitterReleaseLatchHoldsBuildAndGuardsPreexistingGoBin(t *testing.T) {
	fixture := newFakeFetch(t, "custom", "ok", true)
	s, parentRead, parentWrite, sessionRead, sessionWrite := newFetchSession(context.Background())
	defer s.cancel()
	defer parentRead.Close()
	defer parentWrite.Close()
	defer sessionRead.Close()
	defer sessionWrite.Close()
	root := filepath.Join(fixture.temp, "checkout")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(fixture.temp, "builds")
	if err := os.MkdirAll(filepath.Join(outDir, "linux-amd64"), 0o755); err != nil {
		t.Fatal(err)
	}
	buildTask := &task{d: deps{root: root, environ: fixture.environ, toolchain: func() (string, error) {
		return fetchToolchainObserved(s.ctx, fixture.hostGo, fixture.environ, s)
	}, beforeBuild: s.beforeBuild}}
	built := make(chan error, 1)
	go func() {
		_, err := buildTask.buildOnce(outDir, io.Discard, "synthetic", "linux/amd64")
		built <- err
	}()
	frame := readTestFrameBounded(t, parentRead)
	if frame.Kind != "RETURN" {
		t.Fatalf("actual fetch return missing: %s", frame.Kind)
	}
	if _, err := os.Stat(fixture.marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("buildOne ran before acknowledgement: %v", err)
	}
	select {
	case err := <-built:
		t.Fatalf("buildOnce returned before RELEASE: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := writeTestFrame(parentWrite, s.nonce, 2, "RELEASE", emptyPayload{}); err != nil {
		t.Fatal(err)
	}
	if err := <-built; err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(fixture.marker); err != nil || !strings.Contains(string(data), "build-started") {
		t.Fatalf("build marker absent after release: %q, %v", data, err)
	}

	blockedOutput := filepath.Join(fixture.temp, "blocked")
	if err := os.MkdirAll(filepath.Join(blockedOutput, "linux-amd64"), 0o755); err != nil {
		t.Fatal(err)
	}
	blocked := &task{d: deps{root: root, environ: fixture.environ, beforeBuild: func(string) error {
		return errors.New("unreleased")
	}}, goBin: fixture.goBin}
	if _, err := blocked.buildOnce(blockedOutput, io.Discard, "blocked", "linux/amd64"); err == nil {
		t.Fatal("already-populated goBin bypassed immediate beforeBuild")
	}
}

func TestGoEmitterSharedToolchainWaitersBothFailOnCancelOrReleaseEOF(t *testing.T) {
	for _, eof := range []bool{false, true} {
		t.Run(fmt.Sprintf("eof-%t", eof), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			fixture := newFakeFetch(t, "custom", "ok", false)
			var session *toolchainSession
			var parentRead *io.PipeReader
			var parentWrite *io.PipeWriter
			var sessionRead *io.PipeReader
			var sessionWrite *io.PipeWriter
			if eof {
				session, parentRead, parentWrite, sessionRead, sessionWrite = newFetchSession(ctx)
				defer parentRead.Close()
				defer parentWrite.Close()
				defer sessionRead.Close()
				defer sessionWrite.Close()
				go func() {
					frame, err := readTestFrame(parentRead)
					if err == nil && frame.Kind == "RETURN" {
						_ = parentWrite.Close()
						_, _ = readTestFrame(parentRead)
					}
				}()
			}
			var count int
			var countMu sync.Mutex
			started := make(chan struct{})
			shared := newSharedToolchain(ctx, func(ctx context.Context) (string, error) {
				countMu.Lock()
				count++
				countMu.Unlock()
				close(started)
				if eof {
					return fetchToolchainObserved(ctx, fixture.hostGo, fixture.environ, session)
				}
				<-ctx.Done()
				return "", ctx.Err()
			})
			results := make(chan error, 2)
			for range 2 {
				go func() { _, err := shared.get(); results <- err }()
			}
			<-started
			if !eof {
				cancel()
			}
			a, b := <-results, <-results
			if a == nil || b == nil || a.Error() != b.Error() {
				t.Fatalf("waiters did not share one error: %v / %v", a, b)
			}
			countMu.Lock()
			gotCount := count
			countMu.Unlock()
			if gotCount != 1 {
				t.Fatalf("acquisition ran %d times", gotCount)
			}
			if _, err := os.Stat(fixture.marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed acquisition reached buildOne: %v", err)
			}
		})
	}
}

func TestGoEmitterOverflowAndAcquisitionFailuresEmitError(t *testing.T) {
	for _, tc := range []struct{ name, proxy, download, version string }{
		{"proxy stdout overflow", "overflow", "ok", "ok"},
		{"proxy stderr overflow", "stderr-overflow", "ok", "ok"},
		{"download stdout overflow", "custom", "overflow", "ok"},
		{"download stderr overflow", "custom", "stderr-overflow", "ok"},
		{"download nonzero", "custom", "fail", "ok"},
		{"download malformed", "custom", "malformed", "ok"},
		{"version stdout overflow", "custom", "ok", "overflow"},
		{"version nonzero", "custom", "ok", "fail"},
		{"version mismatch", "custom", "ok", "wrong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFakeFetch(t, tc.proxy, tc.download, false)
			for i := range fixture.environ {
				if strings.HasPrefix(fixture.environ[i], "EMITTER_VERSION_MODE=") {
					fixture.environ[i] = "EMITTER_VERSION_MODE=" + tc.version
				}
			}
			s, parentRead, parentWrite, sessionRead, sessionWrite := newFetchSession(context.Background())
			defer s.cancel()
			defer parentRead.Close()
			defer parentWrite.Close()
			defer sessionRead.Close()
			defer sessionWrite.Close()
			result := make(chan error, 1)
			go func() {
				_, err := fetchToolchainObserved(s.ctx, fixture.hostGo, fixture.environ, s)
				result <- err
			}()
			frame := readTestFrameBounded(t, parentRead)
			if frame.Kind != "ERROR" {
				t.Fatalf("failed acquisition returned %s", frame.Kind)
			}
			if err := <-result; err == nil {
				t.Fatal("failed acquisition returned goBin")
			}
			if _, err := os.Stat(fixture.marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failure ran a build: %v", err)
			}
		})
	}
}

func TestGoEmitterCanceledEffectiveProxyCommandIsWaitedAndFails(t *testing.T) {
	fixture := newFakeFetch(t, "wait", "ok", false)
	ctx, cancel := context.WithCancel(context.Background())
	s := &toolchainSession{input: io.NopCloser(strings.NewReader("")), output: io.Discard,
		nonce: "cancel-command-123", ctx: ctx, cancel: cancel}
	opened := make(chan *os.File, 1)
	openResolved := false
	var gate *os.File
	defer func() {
		cancel()
		if gate != nil {
			_ = gate.Close()
		}
		if !openResolved {
			reader, _ := os.OpenFile(fixture.gate, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if reader != nil {
				_ = reader.Close()
			}
			select {
			case late := <-opened:
				if late != nil {
					_ = late.Close()
				}
			case <-time.After(time.Second):
				t.Error("synthetic command gate cleanup did not complete")
			}
		}
	}()
	go func() {
		gate, err := os.OpenFile(fixture.gate, os.O_WRONLY, 0)
		if err != nil {
			opened <- nil
			return
		}
		opened <- gate
	}()
	result := make(chan error, 1)
	go func() {
		_, err := fetchToolchainObserved(ctx, fixture.hostGo, fixture.environ, s)
		result <- err
	}()
	select {
	case gate = <-opened:
		openResolved = true
		if gate == nil {
			t.Fatal("synthetic command gate did not open")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("controlled command never reached its gate")
	}
	cancel()
	_ = gate.Close()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled source command succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled source command was not Waited/reaped")
	}
	log, err := os.ReadFile(fixture.log)
	if err != nil || strings.TrimSpace(string(log)) != "env -json GOPROXY GOSUMDB" {
		t.Fatal("canceled session launched later acquisition commands")
	}
}

func TestGoEmitterActualNoOfficialBranchEmitsZeroWantResult(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/mschulkind-oss/yolo-jail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, parentRead, parentWrite, sessionRead, sessionWrite := newFetchSession(ctx)
	defer s.cancel()
	defer parentRead.Close()
	defer parentWrite.Close()
	defer sessionRead.Close()
	defer sessionWrite.Close()
	s.wantCount = 0
	s.planSeen = false
	s.resultSent = false
	d := deps{root: root, environ: []string{"PATH=/not-launched"}, packs: fstest.MapFS{}, planObserved: s.planObserved}
	status := make(chan int, 1)
	go func() { status <- run([]string{"check"}, io.Discard, io.Discard, d) }()
	frame := readTestFrameBounded(t, parentRead)
	if frame.Kind != "RETURN" {
		t.Fatalf("zero-want result kind = %s", frame.Kind)
	}
	var resultFields map[string]json.RawMessage
	if err := json.Unmarshal(frame.Payload, &resultFields); err != nil {
		t.Fatal(err)
	}
	if len(resultFields) != 2 {
		t.Fatalf("NO_ACQUISITION fabricated provider evidence: %s", frame.Payload)
	}
	payload := decodePayload[struct {
		Outcome string          `json:"outcome"`
		Plan    planObservation `json:"plan"`
	}](t, frame)
	if payload.Outcome != "no_acquisition" || payload.Plan.Source != "no-official" || payload.Plan.WantCount != 0 {
		t.Fatalf("zero was not observed from the production branch: %+v", payload)
	}
	if got := <-status; s.finishStatus(got) != 0 {
		t.Fatalf("actual no-official check status = %d", got)
	}
}

func TestGoEmitterMissingProviderAndMissingZeroWantReceiptFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wantCount int
	}{
		{name: "nonempty plan missing provider", wantCount: 1},
		{name: "zero want missing frame", wantCount: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			parentRead, sessionWrite := io.Pipe()
			defer parentRead.Close()
			defer sessionWrite.Close()
			s := &toolchainSession{output: sessionWrite, nonce: "missing-result-123", ctx: ctx, cancel: cancel,
				planSeen: true, wantCount: tc.wantCount}
			status := make(chan int, 1)
			go func() { status <- s.finishStatus(0) }()
			frame := readTestFrameBounded(t, parentRead)
			if frame.Kind != "ERROR" || <-status != 1 {
				t.Fatalf("missing-result route was accepted: frame=%s", frame.Kind)
			}
		})
	}
}

func TestGoEmitterProtocolRejectsDuplicateWrongTypeAndOversizedFrames(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"protocol":1,"nonce":"valid-nonce-123","sequence":1,"kind":"INIT","kind":"INIT","payload":{"verb":"check","deadline_millis":1}}`),
		[]byte(`{"protocol":"1","nonce":"valid-nonce-123","sequence":1,"kind":"INIT","payload":{"verb":"check","deadline_millis":1}}`),
		[]byte(`{"protocol":1,"nonce":"valid-nonce-123","sequence":1,"kind":"INIT","payload":{"verb":"check","deadline_millis":"1"}}`),
	} {
		reader, writer := io.Pipe()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		s := &toolchainSession{input: reader, output: io.Discard, ctx: ctx}
		go func(data []byte) {
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], uint32(len(data)))
			_, _ = writer.Write(header[:])
			_, _ = writer.Write(data)
		}(raw)
		var init initPayload
		if err := s.readExpected("INIT", time.Second, &init); err == nil {
			t.Fatal("malformed/duplicate protocol input accepted")
		}
		cancel()
		_ = reader.Close()
		_ = writer.Close()
	}
	reader, writer := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], maxProtocolFrame+1)
		_, _ = writer.Write(header[:])
	}()
	if _, err := readFrameWithTimeout(reader, time.Second, ctx); err == nil {
		t.Fatal("oversized frame length allocated/accepted")
	}
	_ = reader.Close()
	_ = writer.Close()
}

func TestGoEmitterBlockedFrameWriteHonorsContextDeadline(t *testing.T) {
	t.Run("cancellation closes and joins writer", func(t *testing.T) {
		writer := &closeUnblocksWriter{started: make(chan struct{}), closed: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		s := &toolchainSession{output: writer, nonce: "blocked-write-123", ctx: ctx, cancel: cancel}
		result := make(chan error, 1)
		go func() { result <- s.writeFrame("HELLO", helloPayload{Source: "synthetic"}, maxHelloFrame) }()
		<-writer.started
		cancel()
		if err := <-result; err == nil {
			t.Fatal("canceled blocked frame write succeeded")
		}
		select {
		case <-writer.closed:
		default:
			t.Fatal("blocked frame writer was not closed")
		}
	})
	t.Run("closed control pipe", func(t *testing.T) {
		reader, writer := io.Pipe()
		_ = reader.Close()
		s := &toolchainSession{output: writer, nonce: "closed-write-123", ctx: context.Background()}
		if err := s.writeFrame("DONE", donePayload{ExitStatus: 1}, maxControlFrame); err == nil {
			t.Fatal("closed control pipe accepted a frame")
		}
		_ = writer.Close()
	})
	t.Run("bounded writer cleanup failure", func(t *testing.T) {
		writer := &boundedCloseWriter{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		s := &toolchainSession{output: writer, nonce: "bounded-writer-123", ctx: ctx, cancel: cancel}
		result := make(chan error, 1)
		go func() { result <- s.writeFrame("HELLO", helloPayload{Source: "synthetic"}, maxHelloFrame) }()
		<-writer.started
		cancel()
		if err := <-result; !errors.Is(err, errControlWriterCleanupUnavailable) || !s.isCleanupUnavailable() {
			t.Fatal("unjoined frame writer was not classified unavailable")
		}
		close(writer.release)
		<-writer.finished
	})
}

func TestGoEmitterInitRejectsCallerSuppliedExecutionClaims(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := &toolchainSession{input: reader, output: io.Discard, ctx: ctx}
	go func() {
		payload := json.RawMessage(`{"verb":"check","deadline_millis":1000,"host_os":"forged","downloader":"forged","module":"forged","token":"not-a-credential"}`)
		data, _ := json.Marshal(protocolFrame{Protocol: toolchainProtocolVersion, Nonce: "request-claims-123", Sequence: 1, Kind: "INIT", Payload: payload})
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(data)))
		_, _ = writer.Write(header[:])
		_, _ = writer.Write(data)
	}()
	var init initPayload
	if err := s.readExpected("INIT", time.Second, &init); err == nil {
		t.Fatal("INIT accepted request echoes or credential-like fields")
	}
	_ = reader.Close()
	_ = writer.Close()
}

func TestGoEmitterHelloReportsActualRuntimeAndResolvedFakeGo(t *testing.T) {
	fixture := newFakeFetch(t, "custom", "ok", false)
	t.Setenv("PATH", filepath.Dir(fixture.hostGo))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	parentRead, writer := io.Pipe()
	defer parentRead.Close()
	defer writer.Close()
	s := &toolchainSession{output: writer, nonce: "hello-session-123", ctx: ctx, cancel: cancel}
	result := make(chan error, 1)
	go func() { result <- s.sendHello() }()
	frame := readTestFrameBounded(t, parentRead)
	hello := decodePayload[helloPayload](t, frame)
	cwd, _ := os.Getwd()
	executable, _ := os.Executable()
	if frame.Kind != "HELLO" || hello.CWD != cwd || hello.Executable != executable || hello.HostOS != runtime.GOOS || hello.HostArch != runtime.GOARCH {
		t.Fatal("HELLO did not report actual process context")
	}
	if hello.ProvisionalDownloader != fixture.hostGo || hello.ProvisionalDownloaderStatus != "resolved" || hello.DownloaderBinding.Path != fixture.hostGo || hello.DownloaderBinding.Status != "stat" {
		t.Fatal("HELLO did not resolve the fake PATH command")
	}
	if !hello.Environment["PATH"].Present || hello.Environment["PATH"].Value != filepath.Dir(fixture.hostGo) {
		t.Fatal("HELLO did not report the selected PATH context")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestGoEmitterSelectedEnvironmentWhitelistAndBound(t *testing.T) {
	env, err := selectedEnvironment([]string{"HOME=/synthetic/home", "NIX_CFLAGS_COMPILE=-I/synthetic/include"})
	if err != nil {
		t.Fatal(err)
	}
	if !env["HOME"].Present || env["HOME"].Value != "/synthetic/home" || !env["NIX_CFLAGS_COMPILE"].Present || env["NIX_CFLAGS_COMPILE"].Value != "-I/synthetic/include" {
		t.Fatal("selected environment omitted expected fixture values or presence")
	}
	if env["GOFLAGS"].Present {
		t.Fatal("absent selected key was reported present")
	}
	if err := checkContextEnvironmentSize(map[string]selectedValue{"too-large": {Present: true, Value: strings.Repeat("x", maxContextEnvironment)}}); err == nil {
		t.Fatal("over-bound selected environment was accepted")
	}
	if _, err := selectedEnvironment([]string{"GOAUTH=" + strings.Repeat("x", maxContextEnvironment*2)}); err == nil {
		t.Fatal("oversized incoming selected context was materialized")
	}
}

func TestGoEmitterControlledPrefixAcceptsCheckOnly(t *testing.T) {
	if got := runToolchainSession([]string{"check", "1.2.3"}, io.NopCloser(strings.NewReader("")), io.Discard); got != 2 {
		t.Fatalf("controlled prefix accepted a versioned/user verb: %d", got)
	}
}

func TestGoEmitterNilObserverKeepsOrdinaryFetchPath(t *testing.T) {
	fixture := newFakeFetch(t, "custom", "ok", true)
	got, err := fetchToolchain(fixture.hostGo, fixture.environ)
	if err != nil {
		t.Fatal(err)
	}
	if got != fixture.goBin {
		t.Fatalf("ordinary fetch returned %q, want %q", got, fixture.goBin)
	}
	log, err := os.ReadFile(fixture.log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "mod download -json "+toolchainModule(runtime.GOOS, runtime.GOARCH)) || !strings.Contains(string(log), "version") {
		t.Fatalf("ordinary API did not retain actual acquisition path: %s", log)
	}
}

type closeUnblocksWriter struct {
	started chan struct{}
	closed  chan struct{}
	start   sync.Once
	close   sync.Once
}

func (w *closeUnblocksWriter) Write([]byte) (int, error) {
	w.start.Do(func() { close(w.started) })
	<-w.closed
	return 0, io.ErrClosedPipe
}

func (w *closeUnblocksWriter) Close() error {
	w.close.Do(func() { close(w.closed) })
	return nil
}

type boundedCloseWriter struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
	mu       sync.Mutex
	writes   int
	once     sync.Once
}

func (w *boundedCloseWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	w.mu.Lock()
	w.writes++
	if w.writes == 2 {
		close(w.finished)
	}
	w.mu.Unlock()
	return len(p), nil
}

func (w *boundedCloseWriter) Close() error { return nil }

type writeCountingWriter struct {
	mu     sync.Mutex
	writes int
}

func (w *writeCountingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	w.mu.Unlock()
	return len(p), nil
}

func (w *writeCountingWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writes
}

func TestGoEmitterCancellationWaitsForProducerCleanupBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &toolchainSession{input: io.NopCloser(strings.NewReader("")), output: io.Discard,
		nonce: "cleanup-session-123", ctx: ctx, cancel: cancel}
	cleanupStarted, releaseCleanup := make(chan struct{}), make(chan struct{})
	tmp, err := os.MkdirTemp("", "toolchain-emitter-cleanup-")
	if err != nil {
		t.Fatal("cannot create synthetic cleanup fixture")
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "held"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal("cannot create synthetic cleanup marker")
	}
	shared := newSharedToolchain(ctx, func(ctx context.Context) (string, error) {
		defer os.RemoveAll(tmp)
		defer s.fail("download", toolchainObservation{}, "acquisition_failed")
		<-ctx.Done()
		close(cleanupStarted)
		<-releaseCleanup
		return "", ctx.Err()
	})
	shared.cancel, shared.session = cancel, s
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := shared.get()
			if err != nil {
				s.fail("check", toolchainObservation{}, "check_failed")
			}
			results <- err
		}()
	}
	cancel()
	<-cleanupStarted
	<-shared.joinStarted
	select {
	case <-results:
		t.Fatal("canceled waiter returned before producer cleanup")
	default:
	}
	if _, err := os.Stat(filepath.Join(tmp, "held")); err != nil {
		t.Fatal("producer cleanup fixture disappeared before release")
	}
	close(releaseCleanup)
	first, second := <-results, <-results
	if first == nil || second == nil || first.Error() != second.Error() {
		t.Fatal("waiters did not receive the shared cancellation failure")
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("waiters returned before producer cleanup completed")
	}
	s.stateMu.Lock()
	emitted := s.errorSent
	s.stateMu.Unlock()
	if !emitted {
		t.Fatal("producer failure was not claimed exactly once")
	}
}

func TestGoEmitterConcurrentTerminalFailuresEmitOneFrame(t *testing.T) {
	writer := &writeCountingWriter{}
	s := &toolchainSession{output: writer, nonce: "failure-claim-123", ctx: context.Background()}
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			s.fail("check", toolchainObservation{}, "check_failed")
		}()
	}
	close(start)
	wait.Wait()
	if writer.count() != 2 {
		t.Fatal("concurrent failures did not produce exactly one framed error")
	}
}

func TestGoEmitterCanceledCachedAcquisitionCannotReturnSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	shared := newSharedToolchain(ctx, func(context.Context) (string, error) { return "/synthetic/go", nil })
	got, err := shared.get()
	if err != nil || got != "/synthetic/go" {
		t.Fatal("synthetic acquisition did not complete before cancellation")
	}
	cancel()
	got, err = shared.get()
	if err == nil || got != "" {
		t.Fatal("canceled cached acquisition returned success")
	}
}

func TestGoEmitterFinalStatusRejectsCanceledReleasedSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &toolchainSession{output: io.Discard, nonce: "released-session-123", ctx: ctx, cancel: cancel,
		planSeen: true, wantCount: 1, resultSent: true, released: true, releasedGo: "/synthetic/go"}
	cancel()
	if got := s.finishStatus(0); got == 0 {
		t.Fatal("otherwise completed released session reported success after cancellation")
	}
}

func TestGoEmitterBoundedProducerCleanupFailureIsUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &toolchainSession{input: io.NopCloser(strings.NewReader("")), output: io.Discard,
		nonce: "cleanup-bound-123", ctx: ctx, cancel: cancel}
	cleanupStarted, releaseCleanup := make(chan struct{}), make(chan struct{})
	shared := newSharedToolchain(ctx, func(context.Context) (string, error) {
		close(cleanupStarted)
		<-releaseCleanup
		return "", errors.New("synthetic cleanup failure")
	})
	shared.cancel, shared.session, shared.cleanupWait = cancel, s, 20*time.Millisecond
	result := make(chan error, 1)
	go func() { _, err := shared.get(); result <- err }()
	<-cleanupStarted
	cancel()
	<-shared.joinStarted
	if err := <-result; !errors.Is(err, errProducerCleanupUnavailable) {
		t.Fatal("bounded cleanup timeout was not classified unavailable")
	}
	if !s.isCleanupUnavailable() || s.finishStatus(0) == 0 {
		t.Fatal("unjoined producer cleanup was classified as a successful session")
	}
	close(releaseCleanup)
	<-shared.done
}

func TestGoEmitterCompiledPrefixHelperChild(t *testing.T) {
	if os.Getenv("YOLO_GO_EMITTER_COMPILED_CHILD") != "1" {
		return
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal("compiled child could not establish its checkout")
	}
	packsFS, err := compiledSessionTestPacks(root)
	if err != nil {
		t.Fatal("compiled child could not prepare its synthetic official manifest")
	}
	toolchainSessionTestPacks = packsFS
	os.Args = []string{os.Args[0], "--internal-toolchain-session", "check"}
	main()
}

func TestGoEmitterCompiledPrefixDispatchRunsActualMain(t *testing.T) {
	fixture := newFakeFetch(t, "custom", "ok", false)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("cannot locate source checkout")
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	packsFS, err := compiledSessionTestPacks(root)
	if err != nil {
		t.Fatal("cannot prepare synthetic official manifest")
	}
	entries, err := releasematrix.Manifests(packsFS)
	if err != nil {
		t.Fatalf("synthetic manifest rejected: %v", err)
	}
	if len(releasematrix.WithBinaries(entries)) != 1 {
		t.Fatal("synthetic official manifest was not decoded as a binary entry")
	}
	config, err := os.ReadFile(filepath.Join(root, releasematrix.GoreleaserConfig))
	if err != nil {
		t.Fatal("cannot read release platform config")
	}
	release, err := releasematrix.ReleasePlatforms(config)
	if err != nil || len(releasematrix.Census(root, releasematrix.WithBinaries(entries), release)) != 0 {
		t.Fatal("synthetic official manifest did not produce buildable release wants")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("cannot locate compiled test child")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.short=true", "-test.run=^TestGoEmitterCompiledPrefixHelperChild$")
	cmd.Dir = root
	cmd.Env = replaceTestEnvironment(os.Environ(),
		"PATH="+filepath.Dir(fixture.hostGo),
		"EMITTER_LOG="+fixture.log,
		"EMITTER_TC="+filepath.Join(fixture.temp, "toolchain"),
		"EMITTER_MARKER="+fixture.marker,
		"EMITTER_PROXY_MODE=custom",
		"EMITTER_DOWNLOAD_MODE=ok",
		"EMITTER_GOOS="+runtime.GOOS,
		"EMITTER_GOARCH="+runtime.GOARCH,
		"YOLO_GO_EMITTER_COMPILED_CHILD=1",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal("cannot connect compiled child input")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal("cannot connect compiled child output")
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal("cannot start compiled test child")
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	nonce := "actual-main-prefix-123"
	if err := writeTestFrame(stdin, nonce, 1, "INIT", initPayload{Verb: "check", DeadlineMillis: 25000}); err != nil {
		t.Fatal("compiled child did not accept INIT input")
	}
	helloFrame, err := readTestFrame(stdout)
	if err != nil || helloFrame.Kind != "HELLO" || helloFrame.Sequence != 1 || helloFrame.Nonce != nonce {
		t.Fatal("actual main prefix did not emit HELLO")
	}
	hello := decodePayload[helloPayload](t, helloFrame)
	if hello.Executable != executable || hello.ProvisionalDownloader != fixture.hostGo {
		t.Fatal("HELLO did not bind the compiled child and fake PATH command")
	}
	if err := writeTestFrame(stdin, nonce, 2, "START", emptyPayload{}); err != nil {
		t.Fatal("compiled child did not accept START")
	}
	returnFrame, err := readTestFrame(stdout)
	if err != nil {
		t.Fatal("actual nonempty-wants acquisition did not emit a framed result")
	}
	if returnFrame.Kind != "RETURN" || returnFrame.Sequence != 2 {
		if returnFrame.Kind == "ERROR" {
			failure := decodePayload[errorPayload](t, returnFrame)
			log, _ := os.ReadFile(fixture.log)
			t.Fatalf("actual acquisition failed at controlled stage %s with generic code %s (proxy=%t download=%t)",
				failure.Stage, failure.Code, strings.Contains(string(log), "env -json"), strings.Contains(string(log), "mod download"))
		}
		t.Fatalf("actual nonempty-wants acquisition emitted unexpected frame kind %s", returnFrame.Kind)
	}
	returned := decodePayload[struct {
		Outcome     string               `json:"outcome"`
		Observation toolchainObservation `json:"observation"`
	}](t, returnFrame)
	if returned.Outcome != "provider_return" || returned.Observation.ReturnedGo != filepath.Join(fixture.temp, "toolchain", "bin", "go") {
		t.Fatal("RETURN did not report the actual chosen fake toolchain")
	}
	if _, err := os.Stat(fixture.marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("actual build began before RELEASE")
	}
	if err := writeTestFrame(stdin, nonce, 3, "RELEASE", emptyPayload{}); err != nil {
		t.Fatal("compiled child did not accept RELEASE")
	}
	errorFrame, err := readTestFrame(stdout)
	if err != nil || errorFrame.Kind != "ERROR" || errorFrame.Sequence != 3 {
		t.Fatal("synthetic pin mismatch did not report an ordinary generic check failure")
	}
	failed := decodePayload[errorPayload](t, errorFrame)
	if failed.Code != "check_failed" {
		t.Fatal("ordinary check failure did not use its generic session code")
	}
	if data, err := os.ReadFile(fixture.marker); err != nil || !strings.Contains(string(data), "build-started") {
		t.Fatal("actual build marker was not written after RELEASE")
	}
	doneFrame, err := readTestFrame(stdout)
	if err != nil || doneFrame.Kind != "DONE" || doneFrame.Sequence != 4 {
		t.Fatal("compiled child did not emit final DONE")
	}
	done := decodePayload[donePayload](t, doneFrame)
	if done.ExitStatus != 1 {
		t.Fatal("synthetic pin mismatch did not produce the expected ordinary nonzero DONE status")
	}
	if err := writeTestFrame(stdin, nonce, 4, "FINISH", emptyPayload{}); err != nil {
		t.Fatal("compiled child did not accept FINISH")
	}
	_ = stdin.Close()
	waitErr := cmd.Wait()
	waited = true
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || exit.ExitCode() != done.ExitStatus {
		t.Fatal("compiled child exit did not match its nonzero DONE status")
	}
	log, err := os.ReadFile(fixture.log)
	if err != nil || !strings.Contains(string(log), "mod download -json "+toolchainModule(runtime.GOOS, runtime.GOARCH)) {
		t.Fatal("actual nonempty-wants path did not invoke the fake acquisition command")
	}
}

func compiledSessionTestPacks(root string) (fs.FS, error) {
	config, err := os.ReadFile(filepath.Join(root, releasematrix.GoreleaserConfig))
	if err != nil {
		return nil, err
	}
	release, err := releasematrix.ReleasePlatforms(config)
	if err != nil {
		return nil, err
	}
	binary := "yolo-cglimit"
	for _, platform := range release {
		goos, goarch, _ := strings.Cut(platform, "/")
		chain, err := releasematrix.EmbedChain(root, binary, goos, goarch)
		if err != nil {
			return nil, err
		}
		if chain != nil {
			return nil, errors.New("synthetic build target links the pack manifest embed")
		}
	}
	builds := make(map[string]any, len(release))
	for _, platform := range release {
		builds[platform] = map[string]any{
			"url":    releasematrix.AssetURL(binary, "0.0.0", platform),
			"sha256": strings.Repeat("a", 64),
		}
	}
	manifest, err := json.Marshal(map[string]any{
		"name":        "session",
		"binaries":    map[string]any{binary: builds},
		"host_daemon": map[string]any{"cmd": []string{"{binary:" + binary + "}"}, "publishes": "socket"},
		"jail_daemon": map[string]any{"cmd": []string{"{jail_binary:" + binary + "}"}},
	})
	if err != nil {
		return nil, err
	}
	return fstest.MapFS{
		"fixture/loopholes/session/manifest.jsonc": &fstest.MapFile{Data: manifest, Mode: 0o444},
	}, nil
}

func replaceTestEnvironment(environ []string, replacements ...string) []string {
	replace := make(map[string]bool, len(replacements))
	for _, item := range replacements {
		key, _, _ := strings.Cut(item, "=")
		replace[key] = true
	}
	out := make([]string, 0, len(environ)+len(replacements))
	for _, item := range environ {
		key, _, _ := strings.Cut(item, "=")
		if !replace[key] {
			out = append(out, item)
		}
	}
	return append(out, replacements...)
}

// TestPythonControllerEmitterFixtureChild is an isolated actual-main root for the Python
// controller's successful matching and zero-official fixtures. It is inert unless the exact
// compiled-child guard is set by a test invocation using this root's anchored filter.
func TestPythonControllerEmitterFixtureChild(t *testing.T) {
	if os.Getenv("YOLO_PYTHON_CONTROLLER_EMITTER_CHILD") != "1" {
		return
	}
	if !testing.Short() {
		t.Fatal("Python-controller fixture child requires -test.short=true")
	}
	mode := os.Getenv("YOLO_PYTHON_CONTROLLER_EMITTER_MODE")
	root, err := os.Getwd()
	if err != nil {
		t.Fatal("Python-controller compiled child could not establish its checkout")
	}
	packsFS, wanted, err := pythonControllerFixturePacks(root, mode)
	if err != nil {
		t.Fatalf("Python-controller compiled child could not prepare %s fixture: %v", mode, err)
	}
	if packsFS == nil {
		t.Fatal("Python-controller fixture must install an explicit non-nil packs filesystem")
	}
	telemetryPath := os.Getenv("YOLO_PYTHON_CONTROLLER_EMITTER_TELEMETRY")
	if telemetryPath == "" {
		t.Fatal("Python-controller fixture telemetry path is required")
	}
	telemetry, err := json.Marshal(struct {
		Mode   string                 `json:"mode"`
		Builds []pythonControllerWant `json:"builds"`
	}{Mode: mode, Builds: wanted})
	if err != nil {
		t.Fatal("Python-controller fixture telemetry could not be encoded")
	}
	if err := os.WriteFile(telemetryPath, append(telemetry, '\n'), 0o600); err != nil {
		t.Fatalf("Python-controller fixture telemetry could not be recorded: %v", err)
	}
	toolchainSessionTestPacks = packsFS
	os.Args = []string{os.Args[0], "--internal-toolchain-session", "check"}
	main()
}

type pythonControllerWant struct {
	Binary   string `json:"binary"`
	Platform string `json:"platform"`
	SHA256   string `json:"sha256"`
}

func pythonControllerFixturePacks(root, mode string) (fs.FS, []pythonControllerWant, error) {
	if mode == "no-official" {
		return fstest.MapFS{}, []pythonControllerWant{}, nil
	}
	if mode != "matching" {
		return nil, nil, fmt.Errorf("unsupported fixture mode %q", mode)
	}
	config, err := os.ReadFile(filepath.Join(root, releasematrix.GoreleaserConfig))
	if err != nil {
		return nil, nil, err
	}
	release, err := releasematrix.ReleasePlatforms(config)
	if err != nil {
		return nil, nil, err
	}
	if len(release) == 0 {
		return nil, nil, errors.New("release matrix contains no platforms")
	}
	const binary = "yolo-cglimit"
	const version = "0.0.0"
	const fakeBuildBytes = "synthetic build bytes\n"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fakeBuildBytes)))
	builds := make(map[string]any, len(release))
	wanted := make([]pythonControllerWant, 0, len(release))
	for _, platform := range release {
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok || goos == "" || goarch == "" {
			return nil, nil, fmt.Errorf("invalid release platform %q", platform)
		}
		chain, err := releasematrix.EmbedChain(root, binary, goos, goarch)
		if err != nil {
			return nil, nil, err
		}
		if chain != nil {
			return nil, nil, errors.New("synthetic build target links the pack manifest embed")
		}
		builds[platform] = map[string]any{
			"url":    releasematrix.AssetURL(binary, version, platform),
			"sha256": digest,
		}
		wanted = append(wanted, pythonControllerWant{Binary: binary, Platform: platform, SHA256: digest})
	}
	manifest, err := json.Marshal(map[string]any{
		"name":     "python-controller-fixture",
		"binaries": map[string]any{binary: builds},
		"host_daemon": map[string]any{
			"cmd": []string{"{binary:" + binary + "}"}, "publishes": "socket",
		},
		"jail_daemon": map[string]any{"cmd": []string{"{jail_binary:" + binary + "}"}},
	})
	if err != nil {
		return nil, nil, err
	}
	packsFS := fstest.MapFS{
		"fixture/loopholes/python-controller-fixture/manifest.jsonc": &fstest.MapFile{Data: manifest, Mode: 0o444},
	}
	prepared := &task{d: deps{root: root, packs: packsFS}, verb: "check"}
	if err := prepared.prepare(); err != nil {
		return nil, nil, err
	}
	if len(prepared.problems) != 0 {
		return nil, nil, fmt.Errorf("synthetic manifest census rejected fixture: %v", prepared.problems)
	}
	actual := prepared.collectWants()
	if len(actual) != len(wanted) {
		return nil, nil, fmt.Errorf("synthetic manifest selected %d builds, want complete release set %d", len(actual), len(wanted))
	}
	for index, want := range actual {
		if want.name != wanted[index].Binary || want.platform != wanted[index].Platform || want.sum != digest {
			return nil, nil, errors.New("synthetic manifest wanted set differs from its recorded release targets")
		}
	}
	return packsFS, wanted, nil
}
