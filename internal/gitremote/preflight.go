package gitremote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

// PushPreflight is a client-side candidate, NOT a host-verified remote state or an
// authorization. No caller may transmit it until the host implements its own fresh
// receive-pack advertisement and verifies every old ID before deciding.
type PushPreflight struct {
	Commands         []gitproto.Command
	PushOptions      []string
	OldOIDsUntrusted bool
}

// PreparePush uses git's own dry-run selection through the yolo remote helper. It
// intentionally accepts only explicit destination refs: git's porcelain does not
// provide sufficient evidence to reconstruct implicit, wildcard or matching pushes.
// This is not exposed by ForwardAccess while the host has no verifier.
func PreparePush(remote string, arguments []string, dir string) (PushPreflight, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return preparePushContext(ctx, remote, arguments, dir)
}

func preparePushContext(ctx context.Context, remote string, arguments []string, dir string) (PushPreflight, error) {
	refuse := func(reason string) (PushPreflight, error) {
		return PushPreflight{}, fmt.Errorf("git push preflight: %s; run `git push` with an explicit source:refs/heads/name (or :refs/heads/name to delete) for the broker's live decision", reason)
	}
	if !strings.HasPrefix(remote, "yolo::") || len(remote) == len("yolo::") {
		return refuse("the remote must be routed through the yolo git helper")
	}
	matches, err := effectivePushRouteMatches(ctx, dir, remote)
	if err != nil {
		return PushPreflight{}, preflightGitFailure(err, "cannot resolve Git's effective push URL before contacting the remote", refuse)
	}
	if !matches {
		return refuse("Git URL rewrite via applicable insteadOf or pushInsteadOf changes the helper-routed destination; remove that matching rewrite and retry")
	}
	var specs, cliOptions []string
	for i := 0; i < len(arguments); i++ {
		arg := arguments[i]
		switch {
		case arg == "-o" || arg == "--push-option":
			if i+1 >= len(arguments) {
				return refuse("a push option has no value")
			}
			i++
			cliOptions = append(cliOptions, arguments[i])
		case strings.HasPrefix(arg, "--push-option="):
			cliOptions = append(cliOptions, strings.TrimPrefix(arg, "--push-option="))
		case strings.HasPrefix(arg, "-o") && len(arg) > 2:
			cliOptions = append(cliOptions, arg[2:])
		case strings.HasPrefix(arg, "-"):
			return refuse("unsupported push flag " + arg)
		default:
			specs = append(specs, arg)
		}
	}
	if len(specs) == 0 {
		return refuse("no explicit refspec was supplied")
	}
	for _, s := range specs {
		plain := strings.TrimPrefix(s, "+")
		source, dest, ok := strings.Cut(plain, ":")
		if !ok || dest == "" || strings.ContainsAny(plain, "*^~?[") || (source != "" && strings.HasPrefix(source, "-")) {
			return refuse("ambiguous or unsupported refspec " + s)
		}
		valid, err := validDestination(ctx, dir, dest)
		if err != nil {
			return PushPreflight{}, preflightGitFailure(err, "ambiguous or unsupported refspec "+s, refuse)
		}
		if !valid {
			return refuse("ambiguous or unsupported refspec " + s)
		}
	}
	options := cliOptions
	if len(cliOptions) == 0 {
		out, err := gitOutput(ctx, dir, "config", "--null", "--get-all", "push.pushOption")
		if err != nil {
			if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
				return PushPreflight{}, preflightGitFailure(err, "cannot read effective push.pushOption config", refuse)
			}
		} else {
			for _, option := range bytesToOptions(out) {
				if option == "" {
					options = nil // An empty configured value resets earlier push.pushOption values.
					continue
				}
				options = append(options, option)
			}
		}
	}
	for _, o := range options {
		if strings.ContainsAny(o, "\x00\n") {
			return refuse("an effective push option cannot be represented on the wire")
		}
	}
	// Resolve only explicit sources, before contacting the remote. Git itself remains
	// responsible for deciding whether each ref updates, rejects or is up-to-date.
	resolved := make(map[string]string, len(specs))
	for _, s := range specs {
		source, _, _ := strings.Cut(strings.TrimPrefix(s, "+"), ":")
		if source == "" {
			continue
		}
		id, err := gitOutput(ctx, dir, "rev-parse", "--verify", "--end-of-options", source+"^{object}")
		if err != nil {
			return PushPreflight{}, preflightGitFailure(err, "local source "+source+" has no unambiguous object ID", refuse)
		}
		if !objectID(strings.TrimSpace(string(id))) {
			return refuse("local source " + source + " has no unambiguous object ID")
		}
		resolved[s] = strings.TrimSpace(string(id))
	}
	dryArgs := []string{"push", "--dry-run", "--porcelain"}
	for _, o := range cliOptions {
		dryArgs = append(dryArgs, "--push-option="+o)
	}
	dryArgs = append(dryArgs, remote)
	dryArgs = append(dryArgs, specs...)
	dry, err := gitOutput(ctx, dir, dryArgs...)
	if err != nil {
		return PushPreflight{}, preflightGitFailure(err, "git's helper-driven dry-run failed (the ref may be rejected or the remote may be unreachable)", refuse)
	}
	// The remote's old IDs come from a separate helper-mediated read and are
	// explicitly untrusted. The later host verifier must check a fresh advertisement.
	oldBytes, err := gitOutput(ctx, dir, "ls-remote", "--refs", remote)
	if err != nil {
		return PushPreflight{}, preflightGitFailure(err, "cannot read the remote's candidate old refs through the helper", refuse)
	}
	old := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(oldBytes)), "\n") {
		if line == "" {
			continue
		}
		id, ref, ok := strings.Cut(line, "\t")
		if !ok || !objectID(id) || old[ref] != "" {
			return refuse("remote ref advertisement is ambiguous")
		}
		old[ref] = id
	}
	result := PushPreflight{PushOptions: options, OldOIDsUntrusted: true}
	seen := map[string]bool{}
	lines := strings.Split(strings.TrimSpace(string(dry)), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "To ") || lines[len(lines)-1] != "Done" {
		return refuse("git's dry-run porcelain is incomplete")
	}
	for _, line := range lines[1 : len(lines)-1] {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return refuse("git's dry-run returned an unknown result")
		}
		if fields[0] != "*" && fields[0] != " " && fields[0] != "+" && fields[0] != "-" && fields[0] != "=" {
			return refuse("git rejected or skipped a ref (" + fields[0] + ")")
		}
		src, dst, ok := strings.Cut(fields[1], ":")
		if !ok {
			return refuse("git did not name the selected ref")
		}
		var spec string
		for _, s := range specs {
			a, b, _ := strings.Cut(strings.TrimPrefix(s, "+"), ":")
			if a == src && b == dst {
				if spec != "" {
					return refuse("more than one refspec selected the same ref")
				}
				spec = s
			}
		}
		if spec == "" || seen[spec] {
			return refuse("git selected a ref outside the explicit refspecs")
		}
		seen[spec] = true
		if fields[0] == "=" || strings.Contains(fields[2], "[up to date]") {
			return refuse("an up-to-date ref cannot become a push command")
		}
		id := resolved[spec]
		prior := old[dst]
		if fields[0] == "*" && prior != "" || fields[0] != "*" && prior == "" {
			return refuse("the remote ref changed between dry-run and the read of its old ID")
		}
		if id == "" {
			width := 40
			if prior != "" {
				width = len(prior)
			}
			id = strings.Repeat("0", width)
		}
		if prior == "" {
			prior = strings.Repeat("0", len(id))
		}
		if len(prior) != len(id) {
			return refuse("remote and local object ID formats differ")
		}
		result.Commands = append(result.Commands, gitproto.Command{Old: prior, New: id, Ref: dst})
	}
	if len(seen) != len(specs) || len(result.Commands) == 0 {
		return refuse("git did not select every requested ref")
	}
	return result, nil
}

// urlRewrite is one effective url.<base>.insteadOf or pushInsteadOf entry.
type urlRewrite struct {
	base   string
	prefix string
}

// effectivePushRouteMatches reads Git's effective rewrite list, including included and
// command-scope config, then applies Git's longest-prefix and pushInsteadOf precedence.
// Push and the separate ls-remote read must both remain on the helper URL.
func effectivePushRouteMatches(ctx context.Context, dir, remote string) (bool, error) {
	out, err := gitOutput(ctx, dir, "config", "--null", "--get-regexp", `^url\..*\.(insteadof|pushinsteadof)$`)
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 1 {
			return true, nil
		}
		return false, err
	}
	if len(out) == 0 {
		return true, nil
	}
	var insteadOf, pushInsteadOf []urlRewrite
	for _, record := range bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
		key, value, ok := bytes.Cut(record, []byte{'\n'})
		if !ok {
			return false, fmt.Errorf("malformed Git URL rewrite config entry")
		}
		keyText := string(key)
		lowerKey := strings.ToLower(keyText)
		suffix := ".insteadof"
		target := &insteadOf
		if strings.HasSuffix(lowerKey, ".pushinsteadof") {
			suffix = ".pushinsteadof"
			target = &pushInsteadOf
		} else if !strings.HasSuffix(lowerKey, suffix) {
			continue
		}
		if !strings.HasPrefix(lowerKey, "url.") {
			return false, fmt.Errorf("malformed Git URL rewrite key %q", keyText)
		}
		base := keyText[len("url.") : len(keyText)-len(suffix)]
		*target = append(*target, urlRewrite{base: base, prefix: string(value)})
	}
	pushURL, pushMatched := applyURLRewrite(remote, pushInsteadOf)
	if !pushMatched {
		pushURL, _ = applyURLRewrite(remote, insteadOf)
	}
	fetchURL, _ := applyURLRewrite(remote, insteadOf)
	return pushURL == remote && fetchURL == remote, nil
}

// applyURLRewrite uses the first longest matching prefix, as Git does when config entries tie.
func applyURLRewrite(remote string, rewrites []urlRewrite) (string, bool) {
	longest := -1
	result := remote
	for _, rewrite := range rewrites {
		if len(rewrite.prefix) > longest && strings.HasPrefix(remote, rewrite.prefix) {
			longest = len(rewrite.prefix)
			result = rewrite.base + remote[len(rewrite.prefix):]
		}
	}
	return result, longest >= 0
}

func objectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validDestination(ctx context.Context, dir, s string) (bool, error) {
	if !(strings.HasPrefix(s, "refs/heads/") || strings.HasPrefix(s, "refs/tags/")) {
		return false, nil
	}
	_, err := gitOutput(ctx, dir, "check-ref-format", s)
	return err == nil, err
}
func bytesToOptions(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(bytes.TrimSuffix(b, []byte{0}), []byte{0})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, string(p))
	}
	return out
}

// Every Git subprocess shares the caller's absolute deadline. The capture is capped
// across both streams, including a helper that floods before producing any refs.
const maxPreflightOutput = 1 << 20

// killOwnedGitGroup is called only while the leader is unreaped. Its PID still
// reserves the process-group ID, so a negative signal cannot hit a reused group.
func killOwnedGitGroup(pid int, signal func(int, syscall.Signal) error) error {
	return signal(-pid, syscall.SIGKILL)
}

var gitPreflightGroupSignal = syscall.Kill

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return gitOutputWithGroupSignal(ctx, dir, gitPreflightGroupSignal, args...)
}

func gitOutputWithGroupSignal(ctx context.Context, dir string, signal func(int, syscall.Signal) error, args ...string) ([]byte, error) {
	return gitOutputWithReapHook(ctx, dir, signal, nil, args...)
}

func gitOutputWithReapHook(ctx context.Context, dir string, signal func(int, syscall.Signal) error, beforeReap func(), args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c := exec.Command("git", args...)
	c.Dir = dir
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	c.Stdout, c.Stderr = writer, writer
	if err := c.Start(); err != nil {
		_ = writer.Close()
		return nil, err
	}
	_ = writer.Close()
	var groupCancel sync.Once
	var groupSignalErr error
	cancelGroup := func() { groupCancel.Do(func() { groupSignalErr = killOwnedGitGroup(c.Process.Pid, signal) }) }
	readDone := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			// Complete the owned group signal before closing an inherited FD.
			cancelGroup()
			_ = reader.Close()
		case <-readDone:
		}
	}()
	var out bytes.Buffer
	_, readErr := io.CopyN(&out, reader, maxPreflightOutput+1)
	if out.Len() > maxPreflightOutput {
		cancelGroup()
		readErr = errors.New("git preflight output exceeds limit")
	}
	close(readDone)
	<-watchDone
	// This is the final cancellation decision while Git's leader still reserves
	// its process group. Never resample ctx after Wait: a later deadline cannot
	// truthfully claim to have signaled a group whose identity has been released.
	outcomeErr := ctx.Err()
	if outcomeErr != nil {
		cancelGroup()
	} else if readErr == nil || errors.Is(readErr, io.EOF) {
		// EOF alone does not imply Git exited. Observe its exit without reaping
		// so a deadline can still signal the reserved group before Cmd.Wait.
		if err := waitUnreapedGit(ctx, c.Process.Pid); err != nil {
			outcomeErr = err
			cancelGroup()
		} else if err := ctx.Err(); err != nil {
			// A deadline coincident with observing exit still cancels before reap.
			outcomeErr = err
			cancelGroup()
		}
	}
	if groupSignalErr != nil {
		// A denied group kill cannot justify an unbounded Cmd.Wait. Poll only
		// briefly while the group identity is reserved; release the live process
		// handle if it does not exit, and disclose that it remains unreaped.
		return nil, failedGitGroupSignal(c, groupSignalErr, outcomeErr, readErr)
	}
	if beforeReap != nil {
		beforeReap()
	}
	waitErr := c.Wait()
	if outcomeErr != nil {
		return nil, outcomeErr
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return append([]byte(nil), out.Bytes()...), nil
}
