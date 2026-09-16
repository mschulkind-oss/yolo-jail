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

// localProviderForwards returns the host-loopback ports user providers require.
// The existing forward_host_ports transport then presents each one at the same
// localhost port inside a bridged jail. Keeping the URL unchanged is intentional:
// it works across podman, Apple Container, and any future container backend without
// putting a runtime-specific gateway hostname in user configuration.
func localProviderForwards(providers *jsonx.OrderedMap) []any {
	if providers == nil {
		return nil
	}
	seen := map[int]bool{}
	var out []any
	add := func(v any) {
		s, ok := v.(string)
		if !ok {
			return // config validation names malformed URLs before a launch reaches here.
		}
		port, ok := hostLoopbackURLPort(s)
		if !ok || seen[port] {
			return
		}
		seen[port] = true
		out = append(out, port)
	}
	for _, providerName := range providers.Keys() {
		provider, ok := providers.Get(providerName)
		if !ok {
			continue
		}
		entry, ok := provider.(*jsonx.OrderedMap)
		if !ok {
			continue
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
