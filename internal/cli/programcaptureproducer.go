package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
	"golang.org/x/sys/unix"
)

const programCaptureJailVerb = "program-capture-jail"

const maxProgramCaptureDiagnostic = 1024

// Records are withheld until their credential markers and terminal sequences are
// complete. Oversized records are omitted, never truncated before redaction.
const maxProgramCaptureDiagnosticRecord = 4096

var programCaptureAuthorizationRedactor = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[^\s,;]+`)

var programCaptureDiagnosticRedactors = []struct {
	re          *regexp.Regexp
	replacement string
}{
	{programCaptureAuthorizationRedactor, "<redacted>"},
	{regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`), "$1<redacted>@"},
	// Without @, a colon-bearing authority may be unfinished userinfo.
	{regexp.MustCompile(`(?i)(https?://)[^/@\s]+:[^/@\s]*`), "$1<redacted>"},
	{regexp.MustCompile(`(?i)([?&](?:token|key|secret|password|auth)=)[^&\s]+`), "$1<redacted>"},
}

var programCaptureCredentialMarker = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|(?:api[_-]?)?key|token|password|secret)["']?\s*[:=]\s*`)

// Values are scanned only within the already bounded record. An unfinished or
// ambiguous quoted credential omits the record instead of guessing its boundary.
func redactProgramCaptureCredentialRecord(text string) string {
	var safe strings.Builder
	for offset := 0; ; {
		marker := programCaptureCredentialMarker.FindStringIndex(text[offset:])
		if marker == nil {
			safe.WriteString(text[offset:])
			return safe.String()
		}
		start := offset + marker[1]
		if start == len(text) {
			return "[incomplete sensitive producer diagnostic omitted]"
		}
		end := start
		quote := text[start]
		if quote == '"' || quote == '\'' {
			end++
			for end < len(text) && text[end] != quote {
				if text[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(text) {
				return "[incomplete sensitive producer diagnostic omitted]"
			}
			end++
			if end < len(text) && !strings.ContainsRune(" \t\r,;}]", rune(text[end])) {
				return "[ambiguous sensitive producer diagnostic omitted]"
			}
		} else if authorization := programCaptureAuthorizationRedactor.FindStringIndex(text[start:]); authorization != nil && authorization[0] == 0 {
			// Consume scheme and credential together before removing the key's value.
			end = start + authorization[1]
		} else {
			for end < len(text) && !strings.ContainsRune(" \t\r,;", rune(text[end])) {
				if text[end] == '\'' || text[end] == '"' {
					return "[ambiguous sensitive producer diagnostic omitted]"
				}
				end++
			}
		}
		if end == start {
			return "[incomplete sensitive producer diagnostic omitted]"
		}
		safe.WriteString(text[offset:start])
		if quote == '"' || quote == '\'' {
			safe.WriteByte(quote)
		}
		safe.WriteString("<redacted>")
		if quote == '"' || quote == '\'' {
			safe.WriteByte(quote)
		}
		offset = end
	}
}

type programCaptureDiagnostic struct {
	mu   sync.Mutex
	tail []byte // Only sanitized, redacted bytes; shared by the four public streams.
}

func (d *programCaptureDiagnostic) publish(out io.Writer, text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := []byte(text)
	if d.tail == nil {
		d.tail = make([]byte, 0, maxProgramCaptureDiagnostic)
	}
	if len(p) >= maxProgramCaptureDiagnostic {
		d.tail = append(d.tail[:0], p[len(p)-maxProgramCaptureDiagnostic:]...)
	} else {
		drop := len(d.tail) + len(p) - maxProgramCaptureDiagnostic
		if drop > 0 {
			d.tail = append(d.tail[:0], d.tail[drop:]...)
		}
		d.tail = append(d.tail, p...)
	}
	n, err := io.WriteString(out, text)
	if err == nil && n != len(text) {
		err = io.ErrShortWrite
	}
	return err
}

func (d *programCaptureDiagnostic) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Tail truncation may cut a (safe) UTF-8 rune, but must never expose invalid bytes.
	return strings.Join(strings.Fields(strings.ToValidUTF8(string(d.tail), "")), " ")
}

type programCaptureDiagnosticTee struct {
	mu          sync.Mutex
	out         io.Writer
	diagnostic  *programCaptureDiagnostic
	record      []byte
	omitted     bool
	escape      byte // ESC, CSI, string control, string ESC, or ESC intermediates.
	utf8Pending [utf8.UTFMax]byte
	utf8Count   int
	writeErr    error
}

func (w *programCaptureDiagnosticTee) emit(newline bool) error {
	text := ""
	if w.omitted {
		text = "[oversized producer diagnostic omitted]"
	} else {
		text = redactProgramCaptureCredentialRecord(string(w.record))
		for _, redactor := range programCaptureDiagnosticRedactors {
			text = redactor.re.ReplaceAllString(text, redactor.replacement)
		}
	}
	w.record = w.record[:0]
	w.omitted = false
	if newline {
		text += "\n"
	}
	if text == "" {
		return nil
	}
	return w.diagnostic.publish(w.out, text)
}

func (w *programCaptureDiagnosticTee) consume(r rune) error {
	// Accept both the UTF-8 and single-byte representations of C1 terminal controls.
	switch r {
	case 0x9b:
		w.escape = 'c'
		return nil
	case 0x90, 0x9d, 0x9e, 0x9f, 0x98:
		w.escape = 's'
		return nil
	case 0x9c:
		w.escape = 0
		return nil
	}
	switch w.escape {
	case 'e':
		switch r {
		case '[':
			w.escape = 'c'
		case ']', 'P', '_', '^', 'X':
			w.escape = 's'
		default:
			if r >= 0x20 && r <= 0x2f {
				w.escape = 'i'
			} else if r != 0x1b {
				w.escape = 0
			}
		}
		return nil
	case 'i':
		if r >= 0x30 && r <= 0x7e {
			w.escape = 0
		} else if r == 0x1b {
			w.escape = 'e'
		}
		return nil
	case 'c':
		if r >= 0x40 && r <= 0x7e {
			w.escape = 0
		} else if r == 0x1b {
			w.escape = 'e'
		}
		return nil
	case 's':
		if r == 0x07 {
			w.escape = 0
		} else if r == 0x1b {
			w.escape = 't'
		}
		return nil
	case 't':
		if r == '\\' || r == 0x07 {
			w.escape = 0
		} else if r != 0x1b {
			w.escape = 's'
		}
		return nil
	}
	if r == 0x1b {
		w.escape = 'e'
		return nil
	}
	if r == '\n' {
		return w.emit(true)
	}
	if termsafe.Unsafe(r) && r != '\t' && r != '\r' {
		return nil
	}
	if !w.omitted {
		if len(w.record)+utf8.RuneLen(r) > maxProgramCaptureDiagnosticRecord {
			w.record = w.record[:0]
			w.omitted = true
		} else {
			if w.record == nil {
				w.record = make([]byte, 0, maxProgramCaptureDiagnosticRecord)
			}
			w.record = utf8.AppendRune(w.record, r)
		}
	}
	return nil
}

func (w *programCaptureDiagnosticTee) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	for i, b := range p {
		w.utf8Pending[w.utf8Count] = b
		w.utf8Count++
		for w.utf8Count > 0 && utf8.FullRune(w.utf8Pending[:w.utf8Count]) {
			r, size := utf8.DecodeRune(w.utf8Pending[:w.utf8Count])
			invalid := r == utf8.RuneError && size == 1
			if invalid {
				r = rune(w.utf8Pending[0])
			}
			copy(w.utf8Pending[:], w.utf8Pending[size:w.utf8Count])
			w.utf8Count -= size
			if invalid && (r < 0x80 || r > 0x9f) {
				continue
			}
			if err := w.consume(r); err != nil {
				w.writeErr = err
				return i + 1, err
			}
		}
	}
	return len(p), nil
}

func (w *programCaptureDiagnosticTee) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// An incomplete terminal sequence makes the unfinished record unsafe to interpret.
	if w.escape != 0 || w.utf8Count != 0 {
		w.record = w.record[:0]
		w.omitted = false
		return w.writeErr
	}
	if w.writeErr == nil {
		w.writeErr = w.emit(false)
	}
	return w.writeErr
}

type ordinaryCaptureRuntimeKey struct{}

func withOrdinaryCaptureRuntime(ctx context.Context, rt string) context.Context {
	return context.WithValue(ctx, ordinaryCaptureRuntimeKey{}, rt)
}

// Only a normally returned, unbounded child78 can authorize reading a failure payload.
type programCaptureRefused struct{ error }

// programCaptureChild uses the existing bounded owned-child lifecycle, not a second builder.
var programCaptureChild = func(ctx context.Context, workspace string, argv []string, w io.Writer) error {
	child := []string{"internal", programCaptureJailVerb, "--workspace=" + workspace, forkBuildJailStreamsFlag}
	if rt, ok := ctx.Value(ordinaryCaptureRuntimeKey{}).(string); ok && rt != "" {
		child = append(child, "--runtime="+rt)
	}
	child = append(child, "--")
	child = append(child, argv...)
	diag := &programCaptureDiagnostic{}
	if w == nil {
		w = io.Discard
	}
	// Each stream has its own parser: concurrent stderr cannot complete an unfinished
	// stdout credential or escape. The shared diagnostic serializes public publication.
	streams := make([]*programCaptureDiagnosticTee, 4)
	for i := range streams {
		streams[i] = &programCaptureDiagnosticTee{out: w, diagnostic: diag}
	}
	var returned bool
	rc, bound := runCaptureChild(ctx, forkBuildWaitBound, workspace, "ordinary npm", child, captureStreams{out: streams[0], errw: streams[1], jailOut: streams[2], jailErr: streams[3], lifetimeReturned: &returned}, programCaptureEnvironment(os.Environ()))
	var flushErr error
	for _, stream := range streams {
		if err := stream.finish(); err != nil && flushErr == nil {
			flushErr = err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if rc != 0 || bound {
		failure := fmt.Errorf("selected sealed npm producer failed (status %d) — retry preparation", rc)
		if detail := diag.String(); detail != "" {
			failure = fmt.Errorf("selected sealed npm producer failed (status %d): %s — retry preparation", rc, detail)
		}
		if rc == provision.RefusedStatus && !bound && returned {
			return programCaptureRefused{failure}
		}
		return failure
	}

	if flushErr != nil {
		return fmt.Errorf("selected sealed npm producer diagnostic could not be published — repair the output writer, then retry preparation: %w", flushErr)
	}
	return nil
}

func programCaptureEnvironment(environ []string) []string {
	allowed := map[string]bool{"HOME": true, "PATH": true, "TMPDIR": true, "LANG": true, "NO_COLOR": true, "USER": true, "LOGNAME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "XDG_RUNTIME_DIR": true, "YOLO_SETUP": true, "YOLO_RUNTIME": true,
		// Facts about this machine's runtime VM, not npm configuration. Without them the
		// producer's own launch refuses a store-path install prefix on a Mac whose podman
		// machine shares /nix (Nightly macOS Integration run 38723632845).
		"YOLO_NIX_HOST_DAEMON": true, "YOLO_NIX_HOST_STORE_LINUX": true}
	var out []string
	for _, item := range environ {
		name, value, ok := strings.Cut(item, "=")
		if ok && (allowed[name] || name == "NIX_REMOTE" && value == "daemon") {
			out = append(out, item)
		}
	}
	return out
}

func runProgramCaptureJail(args []string, out, w io.Writer) int {
	workspace, rt := "", ""
	streams := false
	var argv []string
	for i, a := range args {
		if a == "--" {
			argv = args[i+1:]
			break
		}
		switch {
		case strings.HasPrefix(a, "--workspace="):
			workspace = strings.TrimPrefix(a, "--workspace=")
		case strings.HasPrefix(a, "--runtime="):
			rt = strings.TrimPrefix(a, "--runtime=")
		case a == forkBuildJailStreamsFlag:
			streams = true
		default:
			fmt.Fprintf(w, "program-capture-jail: unexpected argument %q — retry selected preparation\n", a)
			return 2
		}
	}
	if workspace == "" || len(argv) == 0 {
		fmt.Fprintln(w, "usage: yolo internal program-capture-jail --workspace=DIR [--jail-streams] -- COMMAND")
		return 2
	}
	s := captureStreams{out: out, errw: w}
	if streams {
		jo, je, jr, ok := forkBuildJailStreams()
		if !ok {
			fmt.Fprintln(w, "program capture lost its owned streams — retry preparation")
			return 1
		}
		defer jo.Close()
		defer je.Close()
		defer jr.Close()
		s.jailOut, s.jailErr = jo, je
		s.jailReady = func() { _, _ = jr.Write([]byte{'R'}); _ = jr.Close() }
	}
	// An empty, NON-NIL selection is essential: even the owner's pack can contain
	// literal provider credentials, hooks or unrelated installation declarations.
	return runCaptureJail(workspace, "ordinary-npm", argv, &captureSeal{only: []string{}}, s, false, captureAct{runtime: rt})
}

func stageProgramCapture(store *capture.Store, id string) (string, error) {
	staging := store.StagingDir(id)
	cname := runtime.FromWorkspace(staging)
	if captureWorkspaceArtifactsExist(staging, cname) {
		release, safe := forkBuildWorkspaceReclaimable(staging, cname)
		if !safe {
			return "", forkBuildRetained{msg: "selected npm runtime is not known gone — stop its owned jail, then retry preparation"}
		}
		cleanupForkBuildWorkspace(staging, cname)
		release()
	}
	return store.Stage(id)
}

func programCaptureRuntimeGone(workspace string) (func(), error) {
	if !forkBuildRunReturned(workspace) {
		return nil, forkBuildRetained{msg: "selected npm runtime has no return witness — stop its owned jail before retrying"}
	}
	release, safe := forkBuildWorkspaceReclaimable(workspace, runtime.FromWorkspace(workspace))
	if !safe {
		return nil, forkBuildRetained{msg: "selected npm runtime teardown is unknown — wait for its owned jail to stop before retrying"}
	}
	return release, nil
}

// Read only bounded regular failure files through the existing no-follow ancestor walk.
// The root was pinned before execution; the caller also holds the runtime-gone witness.
func readProgramFailureFile(root int, leaf string) ([]byte, error) {
	if !filepath.IsLocal(leaf) {
		return nil, fmt.Errorf("failure file path is not confined")
	}
	parent, err := openParentNoFollow(root, filepath.ToSlash(leaf))
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, filepath.Base(leaf), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), leaf)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("failure payload is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(data) > 16384 {
		return nil, fmt.Errorf("failure payload could not be read within its bound")
	}
	return data, nil
}

func resolveSelectedNPMCapture(ctx context.Context, store *capture.Store, in npmCaptureInput, w io.Writer) (*npmCaptureResolution, error) {
	staging, err := stageProgramCapture(store, "npm-resolve-"+in.declarationIdentity())
	if err != nil {
		return nil, err
	}
	// Host aliases above the freshly staged directory are legitimate. Resolve them
	// only at this trusted pre-child open, then follow no child-controlled component.
	root, err := unix.Open(staging, noFollowDir, 0)
	if err != nil {
		return nil, fmt.Errorf("could not pin the owned producer workspace — repair its permissions, then retry preparation")
	}
	defer unix.Close(root)

	b, _ := json.Marshal(in)
	if err = os.WriteFile(filepath.Join(staging, "input.json"), b, 0644); err != nil {
		return nil, err
	}
	argv := []string{"yolo", "internal", "program-npm", "resolve", path.Join(containerWorkspace, "input.json"), path.Join(containerWorkspace, "resolution.json")}
	if err = programCaptureChild(ctx, staging, argv, w); err != nil {
		var refused programCaptureRefused
		if !errors.As(err, &refused) {
			return nil, err
		}
		release, goneErr := programCaptureRuntimeGone(staging)
		if goneErr != nil {
			return nil, err
		}
		defer release()
		defer cleanupForkBuildWorkspace(staging, runtime.FromWorkspace(staging))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		raw, readErr := readProgramFailureFile(root, "resolution.json")
		input, inputErr := readProgramFailureFile(root, "input.json")
		var result npmCaptureResolution
		var stagedInput npmCaptureInput
		if readErr != nil || inputErr != nil || json.Unmarshal(raw, &result) != nil || json.Unmarshal(input, &stagedInput) != nil || stagedInput != in || result.Input != in {
			return nil, fmt.Errorf("invalid owned npm failure payload — retry selected preparation")
		}
		e := result.NodeFloorUnavailable
		// RawMessage preserves JSON null; it is an absent lock, not success facts.
		if string(result.Lock) == "null" {
			result.Lock = nil
		}
		if e == nil || !e.Valid() || e.Owner != in.Owner || e.Bin != in.Bin || e.Floor != in.NodeFloor || !reflect.DeepEqual(result, npmCaptureResolution{Input: in, NodeFloorUnavailable: e}) {
			return nil, fmt.Errorf("invalid or mixed npm failure payload — retry selected preparation")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, e
	}

	release, err := programCaptureRuntimeGone(staging)
	if err != nil {
		return nil, err
	}
	defer release()
	defer cleanupForkBuildWorkspace(staging, runtime.FromWorkspace(staging))
	raw, err := os.ReadFile(filepath.Join(staging, "resolution.json"))
	if err != nil {
		return nil, err
	}
	var r npmCaptureResolution
	if err = json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.Input != in {
		return nil, fmt.Errorf("selected npm resolver changed installation authority — retry preparation")
	}
	if r.ImageProvides != "" {
		// Kept per image identity, so the next launch on this image asks no producer
		// (programimageverdict.go). A verdict that cannot be kept costs the next launch one
		// producer jail, never a wrong answer.
		if err := recordImageProvides(store, r.ImageIdentity, in.Bin, r.ImageProvides); err != nil {
			fmt.Fprintf(w, "%s: could not keep the image's answer (%v); the next launch on this image asks again\n", in.Bin, err)
		}
	}
	if _, err = r.inputs(); err != nil {
		return nil, err
	}
	return &r, nil
}

func produceSelectedNPMCapture(ctx context.Context, staging string, r npmCaptureResolution, w io.Writer) error {
	inputs, err := r.inputs()
	if err != nil {
		return err
	}
	identity, err := inputs.Identity()
	if err != nil {
		return err
	}
	b, _ := json.Marshal(r)
	if err = os.WriteFile(filepath.Join(staging, "resolution.json"), b, 0644); err != nil {
		return err
	}
	argv := []string{"yolo", "internal", "capture-run", "--out=" + path.Join(containerWorkspace, captureOutLeaf), "--scan-content-refs"}
	for _, artifact := range npmCaptureArtifacts(r.Input, identity) {
		argv = append(argv, "--artifact="+artifact)
	}
	argv = append(argv, "--", "yolo", "internal", "program-npm", "install", path.Join(containerWorkspace, "resolution.json"), identity)
	return programCaptureChild(ctx, staging, argv, w)
}

// prepareSelectedNPMCapture is the resolved ordinary producer's admission callsite.
// The original runtime witness is checked by the established admission adapter.
func prepareSelectedNPMCapture(ctx context.Context, store *capture.Store, r npmCaptureResolution, w io.Writer, guard func(func() error) error) (*capture.Entry, error) {
	inputs, err := r.inputs()
	if err != nil {
		return nil, err
	}
	identity, err := inputs.Identity()
	if err != nil {
		return nil, err
	}
	return prepareProgramCapture(ctx, store, inputs, npmCaptureArtifacts(r.Input, identity), func(ctx context.Context, staging string) error { return produceSelectedNPMCapture(ctx, staging, r, w) }, guard)
}
