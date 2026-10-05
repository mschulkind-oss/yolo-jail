package cli

// hostclaudeview.go is the Claude credential view at the host notch
// (docs/design/claude-login-without-interception.md CL-D27): `yolo host -- claude`, with the jails'
// own switch YOLO_CLAUDE_CREDENTIAL_VIEW=1 in the invoking shell, starts Claude on the machine's
// shared login instead of the user's own. The host broker registers a view for it in a directory
// yolo manages (claudeview.HostLocation: <state dir>/host-agents/<pack>), writes the current access
// token there with no refresh token, and keeps it current; Claude is pointed at that directory by
// CLAUDE_SECURESTORAGE_CONFIG_DIR, the variable CL-D22's bridge already sets in a jail.
//
// OFF BY DEFAULT, and that is OQ-NC7's ruling kept rather than reopened: without the switch, host
// Claude keeps its own login in ~/.claude exactly as before, and with it ~/.claude is still never
// written — the managed store is a second directory, which is OQ-OA3's precedent (`yolo host --
// codex` shares the OpenAI login through a managed home and leaves ~/.codex alone). A view holds
// no refresh token, so the host Claude is "a second refresher of nothing" (§8): it races nothing.
// claudeview.DefaultOn("host") stays false until the measures pass, like every backend's.
//
// NEVER FATAL, like the jail's registration: a launch whose view could not be set up starts with
// nothing changed, Claude on the user's own login, and says why and what to look at.

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostClaudeBrokerEnsure starts the host-wide claude-oauth-broker when none is running, the way a
// jail launch's brokerEnsure does, with its lines on errw (a host agent's stdout is its own). A
// variable so a test can stand in for the daemon; the registration after it is real.
var hostClaudeBrokerEnsure = func(errw io.Writer) error {
	deps := broker.RealDeps()
	deps.Out = errw
	if broker.BrokerIsAlive(deps) {
		return nil
	}
	ensured := broker.EnsureSingleton(deps)
	if ensured.Stale != nil {
		return fmt.Errorf("the host-wide broker is running other settings (%s) and could not be "+
			"restarted", strings.Join(ensured.Stale.Changed, ", "))
	}
	return nil
}

// hostClaudeView returns environ with the launched Claude pointed at its view, when this launch
// gives it one, and environ unchanged otherwise. Three things must hold, and the first that does
// not ends it silently: the switch resolves on for the host; the launched program's own pack
// links Claude's credential through a `shared_credentials` hook (the pack is the Claude the view is
// for, run.ClaudeSharedCredentialDir); and the claude-oauth-broker loophole is active with a pack
// that may run host code (run.HostScopedEndpoints, the jail launch's own predicate).
func hostClaudeView(launch *hostComposition, environ []string, errw io.Writer) []string {
	if config.InJail() || !claudeview.Selected(claudeview.HostRuntime, os.Getenv) {
		return environ
	}
	pack := hostInstallingPack(launch.packs, launch.agent)
	if pack == nil || run.ClaudeSharedCredentialDir([]*packload.Pack{pack}) == "" {
		return environ
	}
	if !slices.Contains(run.HostScopedEndpoints(claudeview.HostRuntime, launch.cfg), broker.BrokerLoopholeName) {
		fmt.Fprintf(errw, "yolo host: %s=1 asks for the machine's shared Claude login, and the "+
			"%s loophole is not active here (disabled in your config, or its pack may not run host "+
			"code), so %s starts on its own login in ~/.claude; `yolo loopholes status` says "+
			"which\n", claudeview.SwitchEnv, broker.BrokerLoopholeName, launch.agent)
		return environ
	}
	loc := claudeview.HostLocation(pack.Name)
	res, err := registerHostClaudeView(loc, errw)
	if err != nil {
		fmt.Fprintf(errw, "yolo host: Warning: could not give %s the machine's shared Claude "+
			"login (%v), so it starts on its own login in ~/.claude. `yolo claude-auth status` "+
			"shows the broker's store; unset %s to stop asking\n", launch.agent, err, claudeview.SwitchEnv)
		return environ
	}
	fmt.Fprintf(errw, "yolo host: %s reads the machine's shared Claude login from a store yolo "+
		"manages, %s, which the host broker keeps current (%s=1); your own ~/.claude is not "+
		"touched\n", launch.agent, homeTilde(loc.Dir), claudeview.SwitchEnv)
	if res.WasLoggedOut {
		fmt.Fprintf(errw, "yolo host: /logout in an earlier `yolo host -- %s` had signed this store "+
			"out of the machine's login; this launch signs it back in. `yolo claude-auth logout` "+
			"signs the whole machine out\n", launch.agent)
	}
	if !res.Wrote {
		fmt.Fprintf(errw, "yolo host: this machine has no shared Claude login yet; /login in this "+
			"session enrolls the machine, for every jail and `yolo host` launch that shares it\n")
	}
	return setEnviron(environ, claudeview.SecureStorageEnv, loc.Dir)
}

// registerHostClaudeView ensures the broker and registers loc with it.
func registerHostClaudeView(loc claudeview.Location, errw io.Writer) (oauthbroker.RegisterResult, error) {
	if err := hostClaudeBrokerEnsure(errw); err != nil {
		return oauthbroker.RegisterResult{}, err
	}
	oauthbroker.ConfigureStore()
	return oauthbroker.RegisterView(loc, claudeview.HostRuntime, "")
}

// hostInstallingPack is the selected pack that installs bin, nil when none does.
func hostInstallingPack(packs []*packload.Pack, bin string) *packload.Pack {
	name := installingPack(packs, bin)
	if name == "" {
		return nil
	}
	for _, p := range packs {
		if p != nil && p.Name == name {
			return p
		}
	}
	return nil
}

// setEnviron is environ with key set to value: every existing entry for key removed, so the child
// sees one value whichever entry its libc reads first, and the new one appended.
func setEnviron(environ []string, key, value string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+value)
}
