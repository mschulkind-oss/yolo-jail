package callbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const cliResponseLimit = 32 << 20

// pinnedCLI is a host-only executor identity; the jail cannot set its path,
// working directory, environment or credential destination. The handler invokes
// it only after the current authority transaction admits a classified call.
type pinnedCLI struct {
	path    string
	sha256  string
	env     []string
	secrets [][]byte
}

var hostEnvName = regexp.MustCompile(`\A[A-Z_][A-Z0-9_]*\z`)

func pinCLI(program string, envFromHost []string) (pinnedCLI, error) {
	if program == "" {
		return pinnedCLI{}, errors.New("call broker program is missing; install the declared host CLI before retrying")
	}
	path, err := exec.LookPath(program)
	if err != nil {
		return pinnedCLI{}, fmt.Errorf("call broker host program %q is unavailable: %w; install it on the host and retry", program, err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return pinnedCLI{}, fmt.Errorf("call broker host program %q changed: %w; reinstall it and retry", program, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return pinnedCLI{}, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return pinnedCLI{}, errors.New("call broker host program is not a regular executable; reinstall it and retry")
	}
	p := pinnedCLI{path: path, env: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}}
	if p.sha256, err = p.checksum(); err != nil {
		return pinnedCLI{}, err
	}
	seen := map[string]bool{"PATH": true, "HOME": true}
	for _, name := range envFromHost {
		if !hostEnvName.MatchString(name) {
			return pinnedCLI{}, errors.New("call broker host environment declaration is invalid; check the selected pack")
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if value, ok := os.LookupEnv(name); ok {
			p.env = append(p.env, name+"="+value)
			if value != "" {
				p.secrets = append(p.secrets, []byte(value))
			}
		}
	}
	sort.SliceStable(p.secrets, func(i, j int) bool { return len(p.secrets[i]) > len(p.secrets[j]) })
	return p, nil
}

type CLIResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

func (p pinnedCLI) checksum() (string, error) {
	f, err := os.Open(p.path)
	if err != nil {
		return "", errors.New("host CLI changed; reinstall it and retry")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("host CLI is no longer executable; reinstall it and retry")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", errors.New("host CLI cannot be checked; reinstall it and retry")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (p pinnedCLI) unchanged() error {
	got, err := p.checksum()
	if err != nil {
		return err
	}
	if got != p.sha256 {
		return errors.New("host CLI changed since this launch; relaunch before retrying")
	}
	return nil
}

type completedExecutionError struct{ err error }

type completedWriteResponseError struct{ err error }

func (e completedExecutionError) Error() string { return e.err.Error() }
func (e completedExecutionError) Unwrap() error { return e.err }

func (e completedWriteResponseError) Error() string { return e.err.Error() }
func (e completedWriteResponseError) Unwrap() error { return e.err }

func completedWriteResponse(err error) bool {
	var completed completedWriteResponseError
	return errors.As(err, &completed)
}

func executionCompleted(err error) bool {
	var completed completedExecutionError
	return errors.As(err, &completed)
}

// run uses only a previously validated canonical call. It cannot be called by
// the jail or classifier: the live daemon must first verify the same digest
// against its store and spend an exact grant in the dispatch transaction.
func (p pinnedCLI) run(ctx context.Context, canonical Classification, originalStdin []byte) (CLIResult, error) {
	if p.path == "" || !filepath.IsAbs(p.path) || len(canonical.Argv) == 0 ||
		canonical.StdinSHA256 != fmt.Sprintf("%x", sha256.Sum256(originalStdin)) {
		return CLIResult{}, errors.New("canonical CLI call or original stdin changed; classify the call again before retrying")
	}
	for _, word := range canonical.Argv {
		if strings.ContainsRune(word, 0) {
			return CLIResult{}, errors.New("canonical CLI argv is invalid; check the selected pack and retry")
		}
	}
	if err := p.unchanged(); err != nil {
		return CLIResult{}, err
	}
	cmd := exec.Command(p.path, canonical.Argv...)
	cmd.Dir = "/"
	cmd.Env = append([]string(nil), p.env...)
	cmd.Stdin = bytes.NewReader(originalStdin)
	var stdout, stderr boundedResponse
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := runOwnedGroup(ctx, cmd)
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return CLIResult{}, fmt.Errorf("host CLI failed: %w; check the selected host program and retry", err)
	}
	if ctx.Err() != nil {
		return CLIResult{}, fmt.Errorf("host CLI timed out or was cancelled: %w; retry after checking the host service", ctx.Err())
	}
	if stdout.tooLarge || stderr.tooLarge {
		return CLIResult{}, completedExecutionError{errors.New("host CLI response exceeded 32 MiB; narrow the requested output and retry")}
	}
	redactedOut, errOut := redactBounded(stdout.Bytes(), p.secrets)
	redactedErr, errErr := redactBounded(stderr.Bytes(), p.secrets)
	if errOut != nil || errErr != nil {
		return CLIResult{}, completedExecutionError{errors.New("host CLI redacted response exceeded 32 MiB; narrow the requested output and retry")}
	}
	result := CLIResult{Stdout: redactedOut, Stderr: redactedErr}
	if err == nil {
		return result, nil
	}
	result.ExitCode = exit.ExitCode()
	return result, nil
}

type boundedResponse struct {
	buffer   bytes.Buffer
	tooLarge bool
}

func (b *boundedResponse) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedResponse) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > cliResponseLimit {
		b.tooLarge = true
		return len(p), nil
	}
	return b.buffer.Write(p)
}

// redactBounded checks expansion before allocating each replacement. No partial
// response crosses the call boundary if even one final stream is oversized.
func redactBounded(output []byte, secrets [][]byte) ([]byte, error) {
	for _, secret := range secrets {
		if len(secret) == 0 {
			continue
		}
		if growth := len("[redacted]") - len(secret); growth > 0 &&
			bytes.Count(output, secret) > (cliResponseLimit-len(output))/growth {
			return nil, errors.New("redacted response exceeds 32 MiB")
		}
		output = bytes.ReplaceAll(output, secret, []byte("[redacted]"))
	}
	return output, nil
}
