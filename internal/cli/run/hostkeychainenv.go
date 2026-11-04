package run

// hostkeychainenv.go is OQ-KC5's macos-user route (docs/design/keychain-from-a-jail.md §4.1,
// ruled 2026-11-03): a native client in the macos-user sandbox cannot reach a keychain (the
// sandbox account has none, and there is no seam yolo can serve), so at launch the HOST side,
// which runs as the human, reads each selected program's declared login item from the human's
// own keychain and hands it to that program through the variable it already reads, in the
// program's own agent env file (agentenvfiles.go) and nowhere else.
//
// WHAT IS READ is a pack's declaration (packdecl's host_keychain_env): packs/copilot names the
// `copilot-cli` service and COPILOT_GITHUB_TOKEN. Core spells no service and no program.
//
// THE SECRET'S PATH. `security find-generic-password -s <service> -w` prints the secret on
// stdout; the service name is the only argv word that is not a flag, and it is no secret. The
// secret is held in memory, written only into the 0600 agent env file, and never printed,
// logged or put on any argv. The tool's stderr is discarded, since nothing in it is needed and a
// diagnostic is never worth a chance of echoing an item's attributes.
//
// EVERY OUTCOME IS ONE LAUNCH LINE (no quiet mode, AGENTS.md; every stop names the next step,
// docs/reference/happy-path-principle.md): the read that worked is disclosed, and a read that
// found nothing or was refused sets nothing and says how to log in.
//
// NO SECOND REFRESHER. Only what a program declares is read, and nothing is written back: the
// program's own client keeps refreshing its login where it always did. Claude declares nothing
// here; its refresh ownership stays the broker's (§4.1).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// ErrHostKeychainItemNotFound is a read that found no item for the service.
var ErrHostKeychainItemNotFound = errors.New("no such item")

// ErrHostKeychainNoDialog is `security` exit 36, errSecInteractionNotAllowed (-25308): macOS
// refused to let the tool show its keychain dialog to this process. It is NOT "the keychain is
// locked": on the owner's Mac (docs/lab-reports/2026-11-03-mac-slice-12a-witness-w1.md §14) the
// login keychain was unlocked and no dialog appeared: a shell reached over ssh (with or without
// tmux) got rc=36 at once, and a Terminal on the Mac's own display got rc=0 after an Allow
// dialog. A locked keychain in such a session fails the same way,
// since unlocking it needs the same dialog, so the one status covers both and the next step is
// the same: a process that can show the dialog. Its launch line carries its own next steps
// (noteHostKeychainOutcome).
var ErrHostKeychainNoDialog = errors.New("macOS refused to let `security` show its keychain access dialog to this process")

// hostKeychainSecurity is macOS's own keychain tool, by absolute path so a PATH entry cannot
// stand in for it.
const hostKeychainSecurity = "/usr/bin/security"

// hostKeychainReadTimeout bounds one read. macOS may show its "allow access" dialog for an item
// another program created, and the human needs time to answer it.
const hostKeychainReadTimeout = 2 * time.Minute

// DefaultHostKeychainRead is the production HostKeychainRead: `security find-generic-password
// -s <service> -w`, as the human, on their default keychain search list. Read-only: it never
// creates, changes or deletes an item.
func DefaultHostKeychainRead(service string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hostKeychainReadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, hostKeychainSecurity, "find-generic-password", "-s", service, "-w")
	var out bytes.Buffer
	cmd.Stdin = nil
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", errors.New("timed out waiting for the keychain")
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", securityExitError(exit.ExitCode())
		}
		return "", fmt.Errorf("could not run %s", hostKeychainSecurity)
	}
	return strings.TrimRight(out.String(), "\r\n"), nil
}

// securityExitError names a `security` exit status: the low byte of the Security.framework
// status it failed with.
func securityExitError(code int) error {
	switch code {
	case 44: // errSecItemNotFound, -25300
		return ErrHostKeychainItemNotFound
	case 36: // errSecInteractionNotAllowed, -25308
		return ErrHostKeychainNoDialog
	case 51: // errSecAuthFailed, -25293
		return errors.New("the keychain refused the request")
	case 128: // userCanceledErr, -128
		return errors.New("the request was declined")
	}
	return fmt.Errorf("security exited %d", code)
}

// hostKeychainOutcome is one declared variable's read.
type hostKeychainOutcome struct {
	agent, variable, login string
	services               []string
	// service is the item that answered, "" when none did.
	service string
	// err is the last failure that was not "no such item", nil when every service was simply
	// absent or one answered; errService is the service whose read failed with it.
	err        error
	errService string
}

// readHostKeychainEnv reads every host_keychain_env declaration of the selected packs through
// o.HostKeychainRead and returns, per agent, the variables filled, printing one line per
// declared variable. nil, and no line, when o.HostKeychainRead is unset (a hand-built Options)
// or no selected pack declares any. The caller runs it on a macos-user launch only.
func (o *Options) readHostKeychainEnv(packs []*packload.Pack) map[string]map[string]string {
	if o.HostKeychainRead == nil {
		return nil
	}
	var out map[string]map[string]string
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, bin := range p.Decl.HostKeychainBins() {
			for _, decl := range p.Decl.HostKeychainEnv(bin) {
				res := hostKeychainOutcome{agent: bin, variable: decl.Var, login: decl.Login, services: decl.Services}
				var secret string
				for _, service := range decl.Services {
					v, err := o.HostKeychainRead(service)
					if err == nil && v != "" && singleLineSecret(v) {
						secret, res.service = v, service
						break
					}
					switch {
					case err == nil && v != "":
						res.err, res.errService = errors.New("the item is not a single-line token"), service
					case err != nil && !errors.Is(err, ErrHostKeychainItemNotFound):
						res.err, res.errService = err, service
					}
				}
				o.noteHostKeychainOutcome(res)
				if res.service == "" {
					continue
				}
				if out == nil {
					out = map[string]map[string]string{}
				}
				if out[bin] == nil {
					out[bin] = map[string]string{}
				}
				out[bin][decl.Var] = secret
			}
		}
	}
	return out
}

// singleLineSecret is a value the env file can carry as one line: no control characters.
func singleLineSecret(v string) bool {
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// noteHostKeychainOutcome prints res's one launch line. It names the service and the variable,
// never the value.
func (o *Options) noteHostKeychainOutcome(res hostKeychainOutcome) {
	out := o.pr(o.Stderr)
	agent := richtext.Escape(res.agent)
	variable := richtext.Escape(res.variable)
	login := "log in with " + agent + " on the host"
	if res.login != "" {
		login = "log in on the host with `" + richtext.Escape(res.login) + "`"
	}
	if res.service != "" {
		out.print("[dim]Host login passed in: your host " + agent + " login (keychain service " +
			richtext.Escape(res.service) + ") is " + variable + " in " + agent +
			"'s own env file in this sandbox; logging out on the host ends it at the next launch.[/dim]")
		return
	}
	services := richtext.Escape(strings.Join(res.services, ", "))
	if res.err == nil {
		out.print("[yellow]No host " + agent + " login in your keychain (service " + services +
			"), so " + agent + " in this sandbox starts logged out: " + login + ", then relaunch.[/yellow]")
		return
	}
	if errors.Is(res.err, ErrHostKeychainNoDialog) {
		probe := shquote.Join([]string{"security", "find-generic-password", "-s", res.errService, "-w"}) +
			" >/dev/null; echo rc=$?"
		// The cause, measured on the owner's Mac (lab report §14, 2026-11-04): rc=36 over ssh,
		// with and without tmux, and rc=0 with a dialog from a Terminal on the Mac's own display.
		// So ssh leads when yolo's own environment shows it; otherwise the general wording.
		cause := "this shell runs in a session outside your desktop login (a terminal or tmux " +
			"server started outside it, for instance), so macOS cannot show the access dialog here"
		if o.Getenv != nil && (o.Getenv("SSH_CONNECTION") != "" || o.Getenv("SSH_TTY") != "") {
			cause = "this shell is reached over ssh, outside your desktop session, so macOS " +
				"cannot show the access dialog here"
		}
		out.print("[yellow]Could not read your host " + agent + " login from your keychain (service " +
			services + ": " + richtext.Escape(res.err.Error()) + "), so " + agent +
			" in this sandbox starts logged out: " + cause + ". Run this launch once from a " +
			"Terminal on the Mac itself and choose Always Allow (not Allow, which is not " +
			"remembered) in the dialog. Then test the read from this shell with `" +
			richtext.Escape(probe) + "`; whether Always Allow also enables later reads over SSH " +
			"has not been measured.[/yellow]")
		return
	}
	out.print("[yellow]Could not read your host " + agent + " login from your keychain (service " +
		services + ": " + richtext.Escape(res.err.Error()) + "), so " + agent +
		" in this sandbox starts logged out: unlock your login keychain and allow the request " +
		"when macOS asks, then relaunch; or " + login + ".[/yellow]")
}
