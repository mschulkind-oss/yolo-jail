package brokerscope

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// file.go is the per-launch scope file (BB-D32): once the config-change gate passes, the
// fresh launch writes the scope that gate approved, the workspace's remotes and its
// `brokered.<source>.repos` entry alike (docs/design/workspace-widening.md WW-P2), to
// broker/<source>/scope/<launch-id>.json and only then spawns the daemon, handing it the
// file's name. For repository authority the daemon reads that file alone — never the
// remotes, approval record or config file (user-only permission-set policy is separate) —
// so no host pre-approval, config edit or other session's
// launch changes a running daemon's scope.
//
// Keyed by a random launch id, never the workspace or the container name: on macos-user
// two terminals in one workspace are two concurrent sessions sharing both, and one
// session's approval must not replace the scope under the other's daemon.

// FileVersion is the scope file's schema version. The remote-url form did not bump it: a file
// without `form` reads as the owner-repo form it always was, and an older build reading a
// remote-url file finds URLs ValidPattern refuses, so it fails closed as a corrupt scope.
const FileVersion = 1

// The remote-url scope form (coined in docs/design/git-broker-plan.md, ID-2): a scope whose
// entries are remote URLs rather than OWNER/REPO. A source declares it with a manifest
// `brokered.remote_host` of AnyRemoteHost; the gate reads it with ReadAllRemotes and presents
// each URL verbatim. Brokers compare exact strings by default; a source may explicitly supply
// a narrower endpoint matcher for its request checks.
const (
	FormRemoteURL = "remote-url"
	// FormCallRoot holds selected call-broker root identifiers exactly, without
	// treating them as GitHub repositories or remote URLs.
	FormCallRoot = "call-root"
	// AnyRemoteHost is loopholedecl.AnyRemoteHost, the `remote_host` that selects the form.
	AnyRemoteHost = "*"
)

// FormFor is the scope form a manifest's `brokered.remote_host` selects.
func FormFor(remoteHost string) string {
	if remoteHost == AnyRemoteHost {
		return FormRemoteURL
	}
	return ""
}

// EntryKey is the spelling a scope entry of form is compared by wherever two scopes are diffed
// (the gate, `yolo config apply`): lower case for OWNER/REPO, as GitHub compares names, and the
// entry verbatim in the remote-url form. A broker's explicit request matcher does not change
// how scope entries are presented or diffed.
func EntryKey(form, entry string) string {
	if form == FormRemoteURL || form == FormCallRoot {
		return entry
	}
	return strings.ToLower(entry)
}

// validEntry is the check a scope entry of form takes, and admits the matcher a request takes
// against one: the form, never the source name, decides both.
func validEntry(form, entry string) bool {
	if form == FormCallRoot {
		return loopholedecl.ValidCallBrokerRoot(entry)
	}
	if form == FormRemoteURL {
		return ValidRemoteURL(entry)
	}
	return ValidPattern(entry)
}

func admits(form, entry, request string) bool {
	if form == FormCallRoot {
		return validEntry(form, request) && entry == request
	}
	if form == FormRemoteURL {
		return ValidRemoteURL(request) && entry == request
	}
	return Admits(entry, request)
}

// File is one launch's scope file.
type File struct {
	BrokerStart string `json:"broker_start,omitempty"`
	BrokerPID   int    `json:"broker_pid,omitempty"`
	Version     int    `json:"version"`
	Source      string `json:"source"`
	LaunchID    string `json:"launch_id"`
	// CallInput is only a host-side locator for the private selected launch input.
	// It is not an approval or a grant; every consumer validates live selection.
	CallInput   string `json:"call_input,omitempty"`
	CallCLIHash string `json:"call_cli_hash,omitempty"`
	CallCLIPath string `json:"call_cli_path,omitempty"`
	// PID is the launching yolo process; the file is collected once that process is known
	// gone (Sweep), never by age.
	PID       int    `json:"pid"`
	Container string `json:"container"`
	Workspace string `json:"workspace"`
	// Form is how Repos' entries read: "" for OWNER/REPO entries and whole-segment wildcards
	// (ValidPattern, Admits), FormRemoteURL for remote URLs (ValidRemoteURL, exact by default).
	// A source may explicitly supply a narrower matcher for a broker request; entry validation,
	// gate comparison and scope-file contents remain verbatim. Every reader branches on Form,
	// never on Source.
	Form string `json:"form,omitempty"`
	// Repos is the approved scope: the workspace's remotes on the forge and its
	// `brokered.<source>.repos` entry, as a human approved them in this launch's config-change
	// gate, from that gate's own result. In the remote-url form, every remote URL of the
	// workspace, verbatim, and no entry.
	Repos []string `json:"repos"`
	// Widened is no longer written (WW-D12). It was the user-scope widening entry's list, read
	// from the user config with no approval, until that form was retired; it stays decodable,
	// and a broker still adds it to the scope, so a file a previous build wrote reads as it did.
	Widened []string `json:"widened,omitempty"`
	// Grants are the user grants the workspace config names, as this launch's gate approved
	// them (grant.go; docs/design/git-broker-plan.md ID-10). Absent from a file an older build
	// wrote, which reads as holding none.
	Grants []Grant `json:"grants,omitempty"`
	// ConfigFile and LocalFile are the names, never paths, of the workspace's config file and
	// local file as the loader reads them (`yolo-jail.jsonc` or the `yolo-jail.json` a project
	// keeps, and its local file), so the broker's out-of-scope refusal names the exact file to
	// edit (WW-D23). Empty from a launch that predates them, and the refusal then says less.
	ConfigFile string `json:"config_file,omitempty"`
	LocalFile  string `json:"local_file,omitempty"`
	// LiveScope says the broker bound to this file classifies every request against the file
	// as it reads it then, so a host `yolo config apply` that rewrites Repos (ReplaceRepos)
	// reaches it at its next request (docs/design/live-config-apply.md §7, LA-D10, LA-D11).
	// BindBroker writes it; a file a broker of an older build bound lacks it, and a scope
	// change there waits for the next restart.
	LiveScope bool `json:"live_scope,omitempty"`
}

// NewLaunchID draws a random launch id.
func NewLaunchID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Write writes a launch's scope file, 0600 in 0700 directories, atomically, and returns its
// path.
func Write(f File) (string, error) {
	// Validate before deriving a lock directory from a source name.
	if f.Source == "" || f.LaunchID == "" || strings.ContainsAny(f.Source+f.LaunchID, "/\\.") {
		return "", errors.New("scope file: bad source or launch id")
	}
	if err := checkGrants(f.Grants); err != nil {
		return "", err
	}
	var path string
	err := brokerlock.WithTransaction(paths.BrokerSourceDir(f.Source), func() error { var err error; path, err = write(f); return err })
	return path, err
}
func write(f File) (string, error) {
	if f.Source == "" || f.LaunchID == "" || strings.ContainsAny(f.LaunchID+f.Source, "/\\.") {
		return "", fmt.Errorf("scope file: bad source %q or launch id %q", f.Source, f.LaunchID)
	}
	f.Version = FileVersion
	if f.Repos == nil {
		f.Repos = []string{}
	}
	path := paths.BrokerScopeFile(f.Source, f.LaunchID)
	dir := filepath.Dir(path)
	for _, d := range []string{paths.BrokerDir(), paths.BrokerSourceDir(f.Source), dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
		fi, err := os.Lstat(d)
		if err != nil {
			return "", err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("scope directory is not real")
		}
		if err = os.Chmod(d, 0700); err != nil {
			return "", err
		}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".scope-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

// ReadFile reads a scope file, refusing a symlink or a file of another version.
func ReadFile(path string) (File, error) {
	var f File
	data, err := readCapped(path, 1<<20)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("scope file %s: %w", path, err)
	}
	if f.Version != FileVersion {
		return f, fmt.Errorf("scope file %s has version %d, this build reads %d", path, f.Version, FileVersion)
	}
	return f, nil
}

// Sweep removes the scope files of a source whose launching process is KNOWN gone: kill(pid,
// 0) answers ESRCH. Any other answer — alive, or not ours to signal — keeps the file, so a
// live launch's file is never collected and a file is never aged out.
func Sweep(source string) (removed []string) {
	if source == "" || strings.ContainsAny(source, "/\\.") {
		return nil
	}
	_ = brokerlock.WithTransaction(paths.BrokerSourceDir(source), func() error { removed = sweep(source); return nil })
	return removed
}
func sweep(source string) (removed []string) {
	dir := filepath.Dir(paths.BrokerScopeFile(source, "x"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		f, err := ReadFile(p)
		if err != nil || f.PID <= 0 {
			continue
		}
		if errors.Is(syscall.Kill(f.PID, 0), syscall.ESRCH) {
			if os.Remove(p) == nil {
				removed = append(removed, p)
			}
		}
	}
	return removed
}

// Remove serializes launch teardown with grant decisions and consumption.
func Remove(path string) error {
	return brokerlock.WithTransaction(filepath.Dir(filepath.Dir(path)), func() error { return os.Remove(path) })
}

// ValidateCurrent is a non-locking gate read, for use inside a store transaction.
// The caller must additionally validate its current policy and broker start identity.
// Matching is exact unless the caller explicitly uses ValidateCurrentWithMatcher.
func ValidateCurrent(path string, expected File, repo string) error {
	return ValidateCurrentWithMatcher(path, expected, repo, nil)
}

// ValidateCurrentWithMatcher performs the same current-file, identity, entry and request
// validation as ValidateCurrent. matcher is consulted only for FormRemoteURL requests, after an
// exact comparison fails; nil preserves the default exact-match behavior for every other caller.
func ValidateCurrentWithMatcher(path string, expected File, repo string, matcher func(entry, request string) bool) error {
	if expected.Source == "" || expected.LaunchID == "" || expected.Workspace == "" || expected.Container == "" || expected.PID <= 0 {
		return errors.New("scope identity unavailable; relaunch the jail")
	}
	f, err := ReadFile(path)
	if err != nil {
		return err
	}
	if f.Form != "" && f.Form != FormRemoteURL && f.Form != FormCallRoot {
		return errors.New("scope file of an unknown form; relaunch the jail")
	}
	for _, r := range append(append([]string(nil), f.Repos...), f.Widened...) {
		if !validEntry(f.Form, r) {
			return errors.New("corrupt repository scope; relaunch the jail")
		}
	}
	if f.Source != expected.Source || f.LaunchID != expected.LaunchID || f.Workspace != expected.Workspace || f.Container != expected.Container || f.PID != expected.PID {
		return errors.New("scope identity changed; relaunch the jail to approve its scope")
	}
	switch {
	case f.Form == FormCallRoot && !validEntry(f.Form, repo):
		return errors.New("invalid call-broker root; retry with an approved root")
	case f.Form == FormRemoteURL && !ValidRemoteURL(repo):
		return errors.New("invalid remote URL; use a remote the workspace's approved scope holds")
	case f.Form == "" && !ValidRepo(repo):
		return errors.New("invalid repository; use an approved owner/repo")
	}
	for _, r := range append(append([]string(nil), f.Repos...), f.Widened...) {
		if admits(f.Form, r, repo) || f.Form == FormRemoteURL && matcher != nil && matcher(r, repo) {
			return nil
		}
	}
	return errors.New("repository no longer approved; relaunch the jail to approve its scope")
}

// BindBroker records a fresh host broker lifetime under the same lock used by decisions.
// No jail field participates in this binding; replacing it invalidates the old lifetime.
// acquireLease runs under this same transaction before binding is published.
func BindBroker(path string, expected File, start string, acquireLease func() error) (File, error) {
	var out File
	if path != paths.BrokerScopeFile(expected.Source, expected.LaunchID) {
		return out, errors.New("scope path is not the host launch path")
	}
	if start == "" {
		return out, errors.New("missing broker lifetime")
	}
	err := brokerlock.WithTransaction(filepath.Dir(filepath.Dir(path)), func() error {
		f, err := ReadFile(path)
		if err != nil {
			return err
		}
		if f.Source != expected.Source || f.LaunchID != expected.LaunchID || f.PID != expected.PID || f.Workspace != expected.Workspace || f.Container != expected.Container {
			return errors.New("scope identity changed before broker start")
		}
		if acquireLease == nil {
			return errors.New("broker lifetime lease unavailable")
		}
		if err := acquireLease(); err != nil {
			return err
		}
		f.BrokerStart, f.BrokerPID = start, os.Getpid()
		// This build's broker re-reads the file for every request (LA-D11).
		f.LiveScope = true
		got, err := write(f)
		if err != nil {
			return err
		}
		if got != path {
			return errors.New("scope path is not its host launch path")
		}
		out = f
		return nil
	})
	return out, err
}

// BindCallInput joins the private input locator to this launch's call-root scope
// before the daemon binds its lifetime. Neither can be supplied by the jail.
func BindCallInput(path string, expect File, input string) error {
	if path != paths.BrokerScopeFile(expect.Source, expect.LaunchID) || input == "" || !filepath.IsAbs(input) {
		return errors.New("call broker input has no host launch binding")
	}
	return brokerlock.WithTransaction(paths.BrokerSourceDir(expect.Source), func() error {
		f, err := ReadFile(path)
		if err != nil {
			return err
		}
		if f.Form != FormCallRoot || f.Source != expect.Source || f.LaunchID != expect.LaunchID ||
			f.Workspace != expect.Workspace || f.Container != expect.Container || f.PID != expect.PID ||
			f.BrokerStart != "" || f.CallInput != "" {
			return errors.New("call broker scope changed before input publication")
		}
		f.CallInput = input
		_, err = write(f)
		return err
	})
}

// BindCallCLI pins the host program's hash to the bound launch, so a separate
// yolo approve process cannot re-pin an executable changed since daemon start.
func BindCallCLI(path string, expect File, cliPath, sha256 string) error {
	if path != paths.BrokerScopeFile(expect.Source, expect.LaunchID) || len(sha256) != 64 || !filepath.IsAbs(cliPath) {
		return errors.New("call broker executable identity is unavailable")
	}
	return brokerlock.WithTransaction(paths.BrokerSourceDir(expect.Source), func() error {
		f, err := ReadFile(path)
		if err != nil {
			return err
		}
		if f.Form != FormCallRoot || f.CallInput != expect.CallInput || f.Source != expect.Source ||
			f.LaunchID != expect.LaunchID || f.BrokerStart != expect.BrokerStart || f.BrokerPID != expect.BrokerPID || f.CallCLIHash != "" || f.CallCLIPath != "" {
			return errors.New("call broker lifetime changed before CLI pin")
		}
		f.CallCLIHash = sha256
		f.CallCLIPath = cliPath
		_, err = write(f)
		return err
	})
}
