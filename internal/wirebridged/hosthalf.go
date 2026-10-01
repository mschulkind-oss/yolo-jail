package wirebridged

// hosthalf.go is `yolo internal daemon wire-bridge`: the bridge's HOST HALF, a launch-owned
// service (docs/design/host-notch-services.md; internal/launchservice). A host launch
// (`yolo host --`, the wrappers) or a macos-user launch starts it as its own child when the one
// agent it runs is paired through the bridge, and stops it when that agent exits.
//
// IT IS THE SAME DAEMON, with its inputs moved off jail paths (the doc's §2.2 warning): the
// wire tables and the caller token come from the launch's 0600 input file rather than the
// per-entry channel, a provider key from that input or this process's environment rather than a
// jail home's key files (keyFor), the Codex route's access-token view from the host broker's
// private socket rather than a jail endpoint file (HS-D3), and no endpoint file is published.
// It serves only its adapter route: a via stays inert outside a jail (WG-I12), and a selection
// that routes nothing here is a launch that should not have started it, so it says why on the
// readiness pipe and exits rather than idling.
//
// Its lifetime is its launch's. SIGTERM (the launch's stop) or the lifeline's EOF (the launch
// died, however) ends it, and it is never restarted.

import (
	"context"
	"net"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
)

// hostHalf reports whether the daemon running under e is the host half.
func hostHalf(e *entrypoint.Env) bool {
	return e != nil && e.Getenv(launchservice.HalfEnv) == ServiceName
}

// listenAt is the listener the daemon under e serves addr on. The host half listens on the port
// its launch reserved for addr and handed it (launchservice.Listen, reading this process's own
// environment, where the launch names the descriptors): the address the agent was composed with,
// held from the pick, so no other listener could be given it first. A jail's bridge binds addr.
func listenAt(e *entrypoint.Env, addr string) (net.Listener, error) {
	if hostHalf(e) {
		return launchservice.Listen(os.Getenv, addr)
	}
	return net.Listen("tcp", addr)
}

// HostMain is the host half's subcommand body. rest is ignored: the whole input is the launch's
// input file, never an argv.
func HostMain(rest []string) int {
	ctx, stop := daemonContext()
	defer stop()
	ctx, cancel := launchservice.Lifeline(ctx, os.Getenv)
	defer cancel()
	e, why := hostHalfEnv(os.Getenv, os.Environ())
	if why != "" {
		logf("the host half cannot start: %s", why)
		signalNotReady(ServiceName, why)
		return 1
	}
	return runHostHalf(ctx, e)
}

// hostHalfEnv is the environment the host half serves by: this process's own, with the launch's
// input over it and the host-half mark set, or why there is none.
func hostHalfEnv(getenv func(string) string, environ []string) (*entrypoint.Env, string) {
	in, err := launchservice.ReadInput(getenv)
	if err != nil {
		return nil, err.Error()
	}
	if in.Service != ServiceName {
		return nil, "the launch's input names service " + in.Service + ", not " + ServiceName
	}
	vars := map[string]string{}
	for _, kv := range environ {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				vars[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	for k, v := range in.Env {
		vars[k] = v
	}
	vars[launchservice.HalfEnv] = ServiceName
	return entrypoint.NewEnv(vars), ""
}

// runHostHalf resolves the one plan the launch's tables select and serves it, adapter route only.
func runHostHalf(ctx context.Context, e *entrypoint.Env) int {
	p := resolvePlan(e)
	p.via = viaPlan{}
	if !p.serves() {
		why := p.idleReason()
		logf("idling is not a host half's state: %s", why)
		signalNotReady(ServiceName, "nothing to serve: "+why)
		return 1
	}
	return servePlan(ctx, p, e)
}
