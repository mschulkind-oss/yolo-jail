// Package clipimage reads one image from the host clipboard, checks it and re-encodes it as a
// fresh PNG, and keeps it in the workspace's paste store, <workspace>/.yolo/paste/
// (docs/design/image-paste.md §7). It is the single place that knows the host's clipboard
// tools: wl-paste and xclip on Linux, osascript on macOS, and the matching writers (wl-copy,
// xclip, pbcopy) `yolo paste` uses to put the saved image's path back on the clipboard.
//
// It runs on the host only. Nothing in a jail can ask it for anything: the caller is a human's
// own command (`yolo paste`) or, later, the terminal proxy's paste arm, both on the host.
package clipimage

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Deadline bounds one whole read of the clipboard (design §7.1): a clipboard owner that does
// not answer counts as a failure.
const Deadline = 2 * time.Second

// The refusals. Each error the package returns wraps one of these, and its text names the next
// step (docs/reference/happy-path-principle.md).
var (
	ErrNoImage         = errors.New("no image on the clipboard")
	ErrNoSession       = errors.New("no graphical session")
	ErrNoReader        = errors.New("no clipboard reader")
	ErrUnsupportedType = errors.New("unsupported image type")
	ErrTooLarge        = errors.New("image too large")
	ErrNotImage        = errors.New("not an image")
	ErrDeadline        = errors.New("clipboard did not answer")
)

// errTooLargeRead is a Runner's answer when a tool wrote more than its limit.
var errTooLargeRead = errors.New("output over the limit")

// ExitError is a tool that ran and exited non-zero.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	if s := strings.TrimSpace(e.Stderr); s != "" {
		return fmt.Sprintf("exit status %d: %s", e.Code, s)
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

// Runner runs one tool. limit > 0 caps stdout (errTooLargeRead past it); limit == 0 discards
// both output streams, which a clipboard WRITER needs: wl-copy and xclip fork a child that keeps
// serving the selection, and a pipe that child inherits would hold the wait open until the
// clipboard changes. stdin nil gives the tool no input.
type Runner func(ctx context.Context, limit int64, stdin []byte, name string, args ...string) ([]byte, error)

// ExecRun is the Runner that executes the tool.
func ExecRun(ctx context.Context, limit int64, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	// A guard against a grandchild holding a pipe open past the tool's own exit; it turns a
	// hang into an error and is never what a paste waits on.
	cmd.WaitDelay = time.Second
	if limit == 0 {
		err := cmd.Run()
		return nil, exitErr(err, "")
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out, rerr := io.ReadAll(io.LimitReader(pipe, limit+1))
	if int64(len(out)) > limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errTooLargeRead
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if werr != nil {
		return nil, exitErr(werr, stderr.String())
	}
	if rerr != nil {
		return nil, rerr
	}
	return out, nil
}

func exitErr(err error, stderr string) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Code: ee.ExitCode(), Stderr: stderr}
	}
	return err
}

// Clip is one image as the clipboard gave it: its bytes, the type they were read as, and the
// tool that read them.
type Clip struct {
	Data []byte
	Type string
	Tool string
}

// Reader reads the host clipboard. Its fields are the host: tests fill them with fakes, and
// HostReader fills them with the process's own.
type Reader struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Run      Runner
}

// accepted is the type preference (design §7.1): PNG, then JPEG, then GIF.
var accepted = []string{"image/png", "image/jpeg", "image/gif"}

// Read returns the clipboard's image, or an error wrapping one of the refusals.
func (r Reader) Read(ctx context.Context) (Clip, error) {
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	tool, err := r.readTool()
	if err != nil {
		return Clip{}, err
	}
	c, err := r.readWith(ctx, tool)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return Clip{}, fmt.Errorf("%w: %s did not answer within %s; copy the image again, then run `yolo paste` again", ErrDeadline, tool, Deadline)
		}
		if errors.Is(err, errTooLargeRead) {
			return Clip{}, tooLargeBytes()
		}
		return Clip{}, err
	}
	return c, nil
}

func tooLargeBytes() error {
	return fmt.Errorf("%w: the clipboard's image is over the %d MiB cap; crop or scale it, copy it again, then run `yolo paste` again", ErrTooLarge, MaxBytes>>20)
}

// readTool picks the reader for this host, or the refusal saying why there is none.
func (r Reader) readTool() (string, error) {
	if r.GOOS == "darwin" {
		if _, err := r.LookPath("osascript"); err != nil {
			return "", fmt.Errorf("%w: osascript is not on PATH, so yolo cannot read the pasteboard; run `yolo paste` from a shell whose PATH has /usr/bin", ErrNoReader)
		}
		return "osascript", nil
	}
	wayland, x11 := r.Getenv("WAYLAND_DISPLAY") != "", r.Getenv("DISPLAY") != ""
	if !wayland && !x11 {
		return "", fmt.Errorf("%w (DISPLAY and WAYLAND_DISPLAY are unset), so there is no clipboard to read; run `yolo paste` in a terminal on your desktop", ErrNoSession)
	}
	if wayland {
		if _, err := r.LookPath("wl-paste"); err == nil {
			return "wl-paste", nil
		}
	}
	if x11 {
		if _, err := r.LookPath("xclip"); err == nil {
			return "xclip", nil
		}
	}
	return "", fmt.Errorf("%w on this host; install wl-clipboard (wl-paste) or xclip, then run `yolo paste` again", ErrNoReader)
}

func noImage(detail string) error {
	if detail != "" {
		detail = " (" + detail + ")"
	}
	return fmt.Errorf("%w%s; copy an image, then run `yolo paste` again", ErrNoImage, detail)
}

func (r Reader) readWith(ctx context.Context, tool string) (Clip, error) {
	switch tool {
	case "osascript":
		return r.readMac(ctx)
	case "wl-paste":
		out, err := r.Run(ctx, MaxBytes, nil, "wl-paste", "--list-types")
		if err != nil {
			return Clip{}, listErr(err)
		}
		typ, err := pickType(out)
		if err != nil {
			return Clip{}, err
		}
		data, err := r.Run(ctx, MaxBytes, nil, "wl-paste", "--no-newline", "--type", typ)
		if err != nil {
			return Clip{}, err
		}
		return Clip{Data: data, Type: typ, Tool: tool}, nil
	default: // xclip
		out, err := r.Run(ctx, MaxBytes, nil, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
		if err != nil {
			return Clip{}, listErr(err)
		}
		typ, err := pickType(out)
		if err != nil {
			return Clip{}, err
		}
		data, err := r.Run(ctx, MaxBytes, nil, "xclip", "-selection", "clipboard", "-t", typ, "-o")
		if err != nil {
			return Clip{}, err
		}
		return Clip{Data: data, Type: typ, Tool: tool}, nil
	}
}

// listErr reads a failed type listing: a tool that ran and said no (an empty clipboard) is "no
// image"; anything else is passed on.
func listErr(err error) error {
	var ee *ExitError
	if errors.As(err, &ee) {
		return noImage(strings.TrimSpace(ee.Stderr))
	}
	return err
}

// pickType chooses from a type listing, one type per line.
func pickType(listing []byte) (string, error) {
	offered := map[string]bool{}
	var otherImages []string
	for _, line := range strings.Split(string(listing), "\n") {
		t := strings.TrimSpace(line)
		offered[t] = true
		if strings.HasPrefix(t, "image/") {
			otherImages = append(otherImages, t)
		}
	}
	for _, t := range accepted {
		if offered[t] {
			return t, nil
		}
	}
	if len(otherImages) > 0 {
		return "", fmt.Errorf("%w: the clipboard offers only %s, which yolo does not read; copy the image as PNG (most screenshot tools and browsers offer it), then run `yolo paste` again",
			ErrUnsupportedType, strings.Join(otherImages, ", "))
	}
	return "", noImage("")
}

// macScript asks the pasteboard for PNG data; the coercion also converts a TIFF screenshot.
// osascript prints the result as «data PNGf<hex>».
const macScript = `the clipboard as «class PNGf»`

func (r Reader) readMac(ctx context.Context) (Clip, error) {
	// Hex doubles the bytes, plus the «data PNGf» wrapper.
	out, err := r.Run(ctx, 2*MaxBytes+64, nil, "osascript", "-e", macScript)
	if err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			// AppleScript's -1700 is "can't make the clipboard into PNG data": no image.
			return Clip{}, noImage(strings.TrimSpace(ee.Stderr))
		}
		return Clip{}, err
	}
	s := strings.TrimSpace(string(out))
	s = strings.TrimPrefix(s, "«data PNGf")
	s = strings.TrimSuffix(s, "»")
	data, err := hex.DecodeString(s)
	if err != nil || len(data) == 0 {
		return Clip{}, fmt.Errorf("%w: osascript returned no PNG data; copy the image again, then run `yolo paste` again", ErrNotImage)
	}
	return Clip{Data: data, Type: "image/png", Tool: "osascript"}, nil
}

// CopyText replaces the host clipboard with text (design §6.4 step 4), with the writer that
// matches this host's reader.
func (r Reader) CopyText(ctx context.Context, text string) error {
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	var argv []string
	switch {
	case r.GOOS == "darwin":
		argv = []string{"pbcopy"}
	case r.Getenv("WAYLAND_DISPLAY") != "" && r.has("wl-copy"):
		argv = []string{"wl-copy"}
	case r.Getenv("DISPLAY") != "" && r.has("xclip"):
		argv = []string{"xclip", "-selection", "clipboard", "-i"}
	default:
		return fmt.Errorf("%w for writing (wl-copy, xclip or pbcopy)", ErrNoReader)
	}
	if !r.has(argv[0]) {
		return fmt.Errorf("%w: %s is not on PATH", ErrNoReader, argv[0])
	}
	_, err := r.Run(ctx, 0, []byte(text), argv[0], argv[1:]...)
	return err
}

func (r Reader) has(name string) bool {
	_, err := r.LookPath(name)
	return err == nil
}

// HostReader is the Reader for this process's own host.
func HostReader(goos string) Reader {
	return Reader{GOOS: goos, Getenv: os.Getenv, LookPath: exec.LookPath, Run: ExecRun}
}
