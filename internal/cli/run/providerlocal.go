package run

// User-declared provider URLs are a host-facing configuration surface: a user who
// writes localhost is naming an inference server on the machine that launches
// yolo, not a service an agent happened to start inside its private network
// namespace. Packs are deliberately excluded — their endpoints are service facts,
// and a pack such as wire-bridge legitimately names jail loopback.

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// providerForward is one implicit host-loopback forward, and WHICH PROVIDER asked for it.
//
// The provider name exists for the disclosure (OQ-PC2,
// docs/design/wire-bridge-port-collision.md): a port the user never wrote is bound inside
// their jail, and a line naming the port without naming who asked for it still leaves them
// grepping their own config for something that is not in it.
type providerForward struct {
	Port     int
	Provider string
}

// localProviderForwardSources returns the implicit forwards WITH their provider names, in
// config order. localProviderForwards is the port-only projection the transport needs.
//
// First writer wins on a duplicate port, which matches the port-only behavior: two
// providers naming one localhost port are one forward, attributed to the one that
// introduced it.
func localProviderForwardSources(providers *jsonx.OrderedMap) []providerForward {
	if providers == nil {
		return nil
	}
	seen := map[int]bool{}
	var out []providerForward
	for _, providerName := range providers.Keys() {
		provider, ok := providers.Get(providerName)
		if !ok {
			continue
		}
		entry, ok := provider.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		add := func(v any) {
			s, ok := v.(string)
			if !ok {
				return
			}
			port, ok := hostLoopbackURLPort(s)
			if !ok || seen[port] {
				return
			}
			seen[port] = true
			out = append(out, providerForward{Port: port, Provider: providerName})
		}
		if base, ok := entry.Get("base_url"); ok {
			add(base)
		}
		endpoints, ok := entry.Get("endpoints")
		if !ok {
			continue
		}
		byProtocol, ok := endpoints.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		for _, protocol := range byProtocol.Keys() {
			endpoint, ok := byProtocol.Get(protocol)
			if !ok {
				continue
			}
			ep, ok := endpoint.(*jsonx.OrderedMap)
			if !ok {
				continue
			}
			if base, ok := ep.Get("base_url"); ok {
				add(base)
			}
		}
	}
	return out
}

// localProviderForwards returns the host-loopback ports user providers require.
// The existing forward_host_ports transport then presents each one at the same
// localhost port inside a bridged jail. Keeping the URL unchanged is intentional:
// it works across podman, Apple Container, and any future container backend without
// putting a runtime-specific gateway hostname in user configuration.
func localProviderForwards(providers *jsonx.OrderedMap) []any {
	sources := localProviderForwardSources(providers)
	if len(sources) == 0 {
		return nil
	}
	out := make([]any, 0, len(sources))
	for _, src := range sources {
		out = append(out, src.Port)
	}
	return out
}

// hostLoopbackURLPort reports the port of an HTTP(S) URL whose hostname is a
// loopback spelling. An absent port means the scheme default, because a URL such
// as http://localhost/v1 still dials the host's TCP port 80.
func hostLoopbackURLPort(raw string) (int, bool) {
	u, err := url.Parse(raw)
	if err != nil || !isHostLoopback(u.Hostname()) {
		return 0, false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return 0, false
		}
		return n, true
	}
	switch u.Scheme {
	case "http":
		return 80, true
	case "https":
		return 443, true
	default:
		return 0, false
	}
}

func isHostLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
