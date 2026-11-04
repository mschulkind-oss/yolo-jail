package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// preparationEnvironment bounds the detached host's inputs. Host source transport keeps its
// authorized helpers; this is not the sealed build environment, which has its own boundary.
// Declarations match case-insensitively so transport aliases cannot bypass credential exclusion.
// Allowed entries retain their original spelling and value.
func preparationEnvironment(in []string, providers *jsonx.OrderedMap, packs []*packload.Pack) []string {
	excluded := map[string]bool{}
	if providers != nil {
		for _, name := range providers.Keys() {
			v, _ := providers.Get(name)
			entry, _ := v.(*jsonx.OrderedMap)
			for _, n := range packload.CredentialEnvNames(entry) {
				excluded[strings.ToUpper(n)] = true
			}
		}
	}
	for _, p := range packs {
		if p.Decl == nil {
			continue
		}
		for _, provider := range p.Decl.Providers() {
			for _, n := range provider.APIKeyEnvName {
				excluded[strings.ToUpper(n)] = true
			}
			for _, n := range provider.RegionEnvName {
				excluded[strings.ToUpper(n)] = true
			}
		}
		for bin := range p.Decl.ProviderEnvByBin {
			for _, v := range p.Decl.ProviderEnv(bin) {
				excluded[strings.ToUpper(v.Name)] = true
			}
		}
	}
	values := make(map[string]string, len(in))
	for _, kv := range packsrc.CleanGitEnv(in) {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || excluded[strings.ToUpper(name)] {
			continue
		}
		switch name {
		// Identity, storage and tool lookup; never workspace or launch-specific inputs.
		case "HOME", "USER", "LOGNAME", "PATH", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
			"TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "TZ":
		// TLS and proxy settings are source transport, not agent provider credentials.
		case "SSL_CERT_FILE", "SSL_CERT_DIR", "NIX_SSL_CERT_FILE", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
			"http_proxy", "https_proxy", "all_proxy", "no_proxy":
		// Git's ordinary host config/helpers and SSH transport remain authorized. Repository-local
		// GIT_CONFIG_* injections and interactive askpass/UI variables are deliberately absent.
		case "SSH_AUTH_SOCK", "SSH_AGENT_PID", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM":
		// Existing source/runtime controls needed by the same sealed-build launcher. No YOLO_ALLOW_*
		// hatches, jail markers, service tokens or arbitrary YOLO_* prefix is inherited.
		case "YOLO_SETUP", "YOLO_RUNTIME", "YOLO_NIX_HOST_DAEMON", "YOLO_NIX_HOST_STORE_LINUX", "NIX_REMOTE", "NIX_DAEMON_SOCKET_PATH", "NIX_STORE_DIR",
			"CONTAINER_HOST", "CONTAINER_CONNECTION", "PODMAN_CONNECTIONS_CONF", "CONTAINERS_CONF", "CONTAINERS_STORAGE_CONF":
		default:
			continue
		}
		values[name] = value
	}
	keys := make([]string, 0, len(values))
	for name := range values {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys)+3)
	for _, name := range keys {
		out = append(out, name+"="+values[name])
	}
	// These are worker-owned controls, not inherited agent variables.
	return append(out, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "SSH_ASKPASS_REQUIRE=never")
}

// loadPreparationEnvironment reads user-authorized declarations before process creation. An
// unreadable boundary must not silently degrade to an inherited or only partly filtered env.
func loadPreparationEnvironment() ([]string, error) {
	return sharedPreparationEnvironment(context.Background())
}

func sharedPreparationEnvironment(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	user, err := config.UserScopeConfig(true, func(string) {})
	if err != nil {
		return nil, preparationEnvironmentError(err)
	}
	problems, _ := config.ValidateProviderSection(user)
	if len(problems) > 0 {
		return nil, preparationEnvironmentError(fmt.Errorf("%s", strings.Join(problems, "; ")))
	}
	var providers *jsonx.OrderedMap
	if v, ok := user.Get("providers"); ok {
		providers, _ = v.(*jsonx.OrderedMap)
	}
	resolver, release := preparationScopedResolver(ctx)
	defer release()
	var contention error
	sel := selectHostPacks(func(e config.PackEntry) (*packload.Pack, error) {
		p, err := resolver(e)
		if errors.Is(err, packsrc.ErrLockHeld) {
			contention = err
		}
		return p, err
	}, config.UserScopeSelection())
	if contention != nil {
		return nil, contention
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if problems := sel.problems(); len(problems) > 0 {
		var reasons []string
		for _, p := range problems {
			reasons = append(reasons, p.Name+": "+p.Reason)
		}
		return nil, preparationEnvironmentError(fmt.Errorf("%s", strings.Join(reasons, "; ")))
	}
	// Shipped declarations exclude known provider inputs even when the provider is not selected;
	// configured declarations add user-defined names without a tool-name denylist.
	packs := append(append([]*packload.Pack(nil), packload.Embedded()...), sel.packs...)
	if problems := packload.EmbeddedProblems(); len(problems) > 0 {
		return nil, preparationEnvironmentError(fmt.Errorf("%s", strings.Join(problems, "; ")))
	}
	return preparationEnvironment(os.Environ(), providers, packs), nil
}

func preparationEnvironmentError(err error) error {
	return fmt.Errorf("cannot bound detached preparation environment: %w — run `yolo check --no-build` and repair the user pack/provider declarations before retrying", err)
}
