package macosuser

// packtreereap.go is the PACK-TREE REAPER (a term coined here): the detached host-user process a
// finishing launch hands its guest pack tree to when a sandbox-account process that may hold the
// tree is still running at the end of the launch's own wait (packtreeproof.go).
//
// # Why a launch hands its tree over
//
// The launch's proof counts every sandbox-account process that started after the tree was
// reserved and is not another running launch's. A session leaves such processes behind in the
// ordinary course: anything the agent or its launcher started in a session of its own (a
// `next-launch` refresh, which is DESIGNED to outlive the program; git's detached auto
// maintenance after a fetch), and anything macOS starts for the account on first use. None of
// them is in the session's process group, so ending that group (the build act's fix, PPX-D49)
// would not reach them, and none is the launch's to kill: the account is every workspace's. So
// the launch keeps no tree and kills nothing. It hands the tree, and its claim, to this process,
// which removes the tree by the same proof once those processes have exited.
//
// # The claim passes with the tree
//
// The launch's claim is an flock on an open file, and a flock belongs to the open file
// description, so the reaper, started with the claim as its fd 3, holds the same lock: the
// launch then lets its own descriptor go without removing the claim (sessionRecord.release).
// So a `yolo prune` running meanwhile sees the tree claimed, as it did while the launch ran, and
// never races the reaper. The reaper rewrites the claim's pid to its own and, when it is done,
// removes the claim and lets the lock go (sessionRecord.close), whatever it decided.
//
// # It gives up
//
// A holder that never exits (a daemon the user left running as the account) would keep a reaper
// forever, so it waits at most packTreeReapBound, then lets the claim go and leaves the tree to
// `yolo prune --apply`, which applies the same proof. Every outcome is one line in its log.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/notty"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// PackTreeReapVerb is the hidden `yolo internal` verb that runs the pack-tree reaper.
const PackTreeReapVerb = "pack-tree-reap"

// packTreeReapClaimFD is the descriptor the reaper finds the claim on: the first of ExtraFiles.
const packTreeReapClaimFD = 3

// packTreeReapBound is how long a reaper waits for the holders before it leaves the tree to
// `yolo prune --apply`; packTreeReapPoll how often it re-reads the process table meanwhile.
// Variables so tests can shorten them.
var (
	packTreeReapBound = 30 * time.Minute
	packTreeReapPoll  = 2 * time.Second
)

// PackTreeReapLog is where every reaper appends what it did: <global storage>/logs.
func PackTreeReapLog() string {
	return filepath.Join(paths.GlobalStorage(), "logs", "macos-user-pack-tree-reap.log")
}

// PackTreeReaperArgv is the reaper's argv: self, the verb, the tree, and the anchor its proof
// counts from (the launch's packTreeUse.anchor, in Unix nanoseconds), and, for a run-as-you
// launch alone, `--mode self`, which decides whose processes are the sandbox's (guestprocs.go).
// An account-mode argv is what it was before the mode existed.
func PackTreeReaperArgv(self, root string, anchor time.Time, mode Mode) []string {
	argv := []string{self, "internal", PackTreeReapVerb,
		"--root", root, "--anchor", strconv.FormatInt(anchor.UnixNano(), 10)}
	if mode == ModeSelf {
		argv = append(argv, "--mode", ModeSelf.String())
	}
	return argv
}

// handOffPackTree starts the reaper for u's tree with u's claim, and on success lets the launch's
// own descriptor of the claim go without removing it, and says in one dim line where the tree
// goes and where the log is. false with a nil error when this launch cannot hand off at all (no
// reaper wired, no claim to pass); false with the error when the start failed.
func handOffPackTree(deps Deps, out printer, u *packTreeUse, holders []GuestProc) (bool, error) {
	if deps.StartPackTreeReaper == nil || deps.SelfExe == nil || u.claim == nil || u.claim.lock == nil {
		return false, nil
	}
	self := deps.SelfExe()
	if self == "" { // a launch whose pinned yolo is gone starts no helper from another build
		return false, nil
	}
	argv := PackTreeReaperArgv(self, u.root, u.anchor, u.mode)
	if err := deps.StartPackTreeReaper(argv, u.claim.lock); err != nil {
		return false, err
	}
	u.claim.release()
	out.printf("[dim]Guest pack tree %s is removed in the background once %s %s exited (log: %s).[/dim]",
		shquote.QuoteDisplay(u.root), describeHolders(holders),
		map[bool]string{true: "has", false: "have"}[len(holders) == 1], PackTreeReapLog())
	return true, nil
}

// errTestBinaryReaper refuses a unit test's binary re-executing itself as the reaper.
var errTestBinaryReaper = errors.New("a test binary does not self-exec the pack-tree reaper")

// startPackTreeReaperReal is Deps.StartPackTreeReaper: argv in a session of its own (no
// controlling terminal, so closing the window does not end it, and no job-control signal meant
// for the shell's foreground reaches it), stdin on /dev/null, stdout and stderr appended to
// PackTreeReapLog (a file, never this process's pipes, or `yolo -- x | tee` would wait for it),
// claim as its fd 3. The Wait runs on a goroutine only to reap a reaper that ends while the
// launcher is still alive; the launcher's exit never waits on it.
func startPackTreeReaperReal(argv []string, claim *os.File) error {
	if len(argv) == 0 {
		return errors.New("empty argv")
	}
	if flag.Lookup("test.v") != nil {
		return errTestBinaryReaper
	}
	logPath := PackTreeReapLog()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	log, err := notty.OpenDetachLog(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	// Not the launcher's working directory: a reaper that waits must not pin the workspace's
	// volume.
	cmd.Dir = "/"
	cmd.Stdout, cmd.Stderr = log, log
	cmd.ExtraFiles = []*os.File{claim}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// packTreeReaper is one reaper run: the tree, the anchor, the claim it inherited, and its seams.
type packTreeReaper struct {
	root   string
	anchor time.Time
	claim  *os.File
	// packsDir is the only directory a tree may sit directly in; claimDir is the claims'.
	packsDir, claimDir string
	procs              func() ([]GuestProc, error)
	run                func([]string) int
	out                io.Writer
	now                func() time.Time
	sleep              func(time.Duration)
}

// PackTreeReapMain is `yolo internal pack-tree-reap --root <tree> --anchor <unix ns>`, with the
// launch's claim on fd 3. Its exit status says only whether it ran: what it did is in its log.
func PackTreeReapMain(args []string) int {
	root, anchor, mode, err := parsePackTreeReapArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "yolo internal "+PackTreeReapVerb+": "+err.Error())
		fmt.Fprintln(os.Stderr, "usage: yolo internal "+PackTreeReapVerb+" --root <guest pack tree> --anchor <unix ns> [--mode self]  (claim on fd 3)")
		return 2
	}
	closeInheritedFiles()
	return reapHandedPackTree(packTreeReaper{
		root:     root,
		anchor:   anchor,
		claim:    os.NewFile(packTreeReapClaimFD, "pack-tree claim"),
		packsDir: filepath.Join(stateDir, packsLeaf),
		claimDir: PackTreeClaimsDir(),
		procs:    func() ([]GuestProc, error) { return guestProcessesReal(mode) },
		run:      runReal,
		out:      os.Stdout,
		now:      time.Now,
		sleep:    time.Sleep,
	})
}

// closeInheritedFiles keeps the claim from the reaper's own children (the unseal shell, chmod,
// rm). Descriptors above it are left alone: Go opens every file of its own close-on-exec, and
// closing one blindly could close the runtime's poller.
func closeInheritedFiles() {
	syscall.CloseOnExec(packTreeReapClaimFD)
}

func parsePackTreeReapArgs(args []string) (string, time.Time, Mode, error) {
	var root, anchor, mode string
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(args[i], "=")
		if !hasVal {
			if i+1 >= len(args) {
				return "", time.Time{}, ModeAccount, fmt.Errorf("%s needs a value", args[i])
			}
			i++
			val = args[i]
		}
		switch name {
		case "--root":
			root = val
		case "--anchor":
			anchor = val
		case "--mode":
			mode = val
		default:
			return "", time.Time{}, ModeAccount, fmt.Errorf("unknown flag %q", name)
		}
	}
	if root == "" || anchor == "" {
		return "", time.Time{}, ModeAccount, errors.New("--root and --anchor are both required")
	}
	ns, err := strconv.ParseInt(anchor, 10, 64)
	if err != nil {
		return "", time.Time{}, ModeAccount, fmt.Errorf("--anchor %q is not Unix nanoseconds", anchor)
	}
	// --mode is Mode.String(), as the launch that hands the tree writes it; absent is the account.
	m := ModeAccount
	switch mode {
	case "", ModeAccount.String():
	case ModeSelf.String():
		m = ModeSelf
	default:
		return "", time.Time{}, ModeAccount, fmt.Errorf("--mode %q is neither \"self\" nor \"account\"", mode)
	}
	return root, time.Unix(0, ns), m, nil
}

// reapHandedPackTree is the reaper's whole run. It touches only a per-launch tree directly in
// packsDir, and only while it holds that tree's claim: a claim it cannot confirm is the one at
// <claimDir>/<leaf>.lock, held, means it was not handed this tree, and it removes nothing.
func reapHandedPackTree(r packTreeReaper) int {
	say := func(format string, a ...any) {
		fmt.Fprintf(r.out, "%s %s: "+format+"\n",
			append([]any{r.now().Format("2006-01-02 15:04:05"), shquote.QuoteDisplay(r.root)}, a...)...)
	}
	leaf := filepath.Base(r.root)
	if filepath.Clean(r.root) != r.root || filepath.Dir(r.root) != r.packsDir ||
		strings.HasPrefix(leaf, ".") || !strings.Contains(leaf, ".") {
		say("not a guest pack tree in %s; nothing removed", r.packsDir)
		return 2
	}
	claim, err := confirmHandedClaim(r.claim, filepath.Join(r.claimDir, leaf+sessionRecordSuffix))
	if err != nil {
		say("was not handed this tree's claim (%v); nothing removed", err)
		return 2
	}
	defer claim.close()
	if claim.lock.Truncate(0) == nil {
		_, _ = claim.lock.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	deadline := r.now().Add(packTreeReapBound)
	for {
		if _, err := os.Lstat(r.root); errors.Is(err, os.ErrNotExist) {
			say("already gone")
			return 0
		}
		procs, err := r.procs()
		if err != nil {
			say("kept: could not list the sandbox's processes (%v); `yolo prune --apply` removes it once none may read it", err)
			return 0
		}
		others := readPackTreeClaims(r.claimDir).pids
		delete(others, os.Getpid())
		holders := packTreeHolders(procs, 0, others, r.anchor)
		if len(holders) == 0 {
			if removeSealedTree(r.run, r.root) {
				say("removed")
				return 0
			}
			say("could not remove it; remove it with `%s -R -- u+w %s && %s -rf -- %s`",
				chmodBin, shquote.QuoteDisplay(r.root), rmBin, shquote.QuoteDisplay(r.root))
			return 0
		}
		if !r.now().Before(deadline) {
			say("kept: %s still may read it after %s; `yolo prune --apply` removes it once %s exited",
				describeHolders(holders), packTreeReapBound,
				map[bool]string{true: "it has", false: "they have"}[len(holders) == 1])
			return 0
		}
		r.sleep(packTreeReapPoll)
	}
}

// confirmHandedClaim checks that f is the claim at path and that this process holds its lock
// (a flock already held through the same open file description is granted again at once), and
// returns it as a record this process now ends.
func confirmHandedClaim(f *os.File, path string) (*sessionRecord, error) {
	if f == nil {
		return nil, errors.New("no descriptor")
	}
	held, err := f.Stat()
	if err != nil {
		return nil, err
	}
	atPath, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(held, atPath) {
		return nil, fmt.Errorf("fd %d is not %s", f.Fd(), path)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("its lock is not this process's: %w", err)
	}
	// Asked again under the lock: a claim its holder removed in between is no claim.
	if again, err := os.Lstat(path); err != nil || !os.SameFile(held, again) {
		return nil, fmt.Errorf("%s changed while it was being locked", path)
	}
	return &sessionRecord{path: path, lock: f}, nil
}
