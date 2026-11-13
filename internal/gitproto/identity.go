package gitproto

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Identity reduces a git remote URL to its repository identity, the host/path string grants
// and working-set entries name a repository by (docs/reference/git-broker.md ID-3): the host
// lower-cased, a port kept as host:port, the user-info dropped, a leading "/" and then a
// leading "~/" stripped from the path, then a trailing "/" and a trailing ".git". So
// git@github.com:o/r.git, ssh://git@github.com/o/r and https://github.com/o/r.git are all
// github.com/o/r.
//
// It accepts the forms the jail's git rewrites to the broker: ssh://, https://,
// git+ssh://, ssh+git:// and scp-style [user@]host:path. Anything else is an error: another
// scheme (http://, git://, file://), a local path, a host or user starting with "-" (which ssh
// would read as an option), a query or fragment, and a path that is empty or holds an empty,
// "." or ".." segment or a space or control byte.
func Identity(remote string) (string, error) {
	host, path, err := splitRemote(remote)
	if err != nil {
		return "", err
	}
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "~/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" {
		return "", fmt.Errorf("remote %q names no repository path", remote)
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("remote %q has a path with an empty, \".\" or \"..\" segment", remote)
		}
	}
	for i := 0; i < len(path); i++ {
		if path[i] <= ' ' || path[i] == 0x7f || path[i] == '\\' {
			return "", fmt.Errorf("remote %q has a space, backslash or control character in its path", remote)
		}
	}
	return host + "/" + path, nil
}

// splitRemote returns a remote's lower-cased host (with any port) and its raw path.
func splitRemote(remote string) (host, path string, err error) {
	if scheme, _, ok := strings.Cut(remote, "://"); ok {
		switch strings.ToLower(scheme) {
		case "ssh", "https", "git+ssh", "ssh+git":
		default:
			return "", "", fmt.Errorf("remote %q uses %s://; only ssh://, git+ssh://, ssh+git://, https:// and scp-style remotes reach the broker", remote, scheme)
		}
		u, err := url.Parse(remote)
		if err != nil {
			return "", "", fmt.Errorf("remote %q is not a URL: %v", remote, err)
		}
		if u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(remote, "#") {
			return "", "", fmt.Errorf("remote %q carries a query or fragment", remote)
		}
		if u.User != nil && strings.HasPrefix(u.User.Username(), "-") {
			return "", "", fmt.Errorf("remote %q has a user starting with \"-\"", remote)
		}
		if err := checkHost(u.Hostname(), true); err != nil {
			return "", "", fmt.Errorf("remote %q: %v", remote, err)
		}
		if p := u.Port(); p != "" && strings.Trim(p, "0123456789") != "" {
			return "", "", fmt.Errorf("remote %q has a port that is not a number", remote)
		}
		if strings.HasSuffix(u.Host, ":") {
			return "", "", fmt.Errorf("remote %q has an empty port", remote)
		}
		return strings.ToLower(u.Host), u.Path, nil
	}
	colon := scpColon(remote)
	if colon < 0 || strings.Contains(remote[:colon], "/") {
		return "", "", fmt.Errorf("remote %q is a local path, which does not reach the broker", remote)
	}
	userHost, path := remote[:colon], remote[colon+1:]
	h := userHost
	if at := strings.LastIndexByte(userHost, '@'); at >= 0 {
		if strings.HasPrefix(userHost[:at], "-") {
			return "", "", fmt.Errorf("remote %q has a user starting with \"-\"", remote)
		}
		h = userHost[at+1:]
	}
	if err := checkHost(h, true); err != nil {
		return "", "", fmt.Errorf("remote %q: %v", remote, err)
	}
	return strings.ToLower(h), path, nil
}

// scpColon finds the delimiter after a bracketed IPv6 host, rather than one of its colons.
func scpColon(remote string) int {
	colon := strings.IndexByte(remote, ':')
	if colon < 0 {
		return colon
	}
	bracket := strings.LastIndexByte(remote[:colon], '[')
	if bracket < 0 {
		return colon
	}
	close := strings.IndexByte(remote[bracket:], ']')
	if close < 0 {
		return colon
	}
	close += bracket
	if close+1 < len(remote) && remote[close+1] == ':' {
		return close + 1
	}
	return colon
}

// checkHost accepts a DNS name or an IP address. IPv6 literals are bracketed in scp-style
// URLs and unbracketed after net/url has parsed a URL authority.
func checkHost(h string, ipv6 bool) error {
	if h == "" {
		return fmt.Errorf("no host")
	}
	if h[0] == '-' {
		return fmt.Errorf("host %q starts with \"-\"", h)
	}
	if strings.HasPrefix(h, "[") || strings.HasSuffix(h, "]") {
		if !ipv6 || len(h) < 3 || h[0] != '[' || h[len(h)-1] != ']' || net.ParseIP(h[1:len(h)-1]) == nil || !strings.Contains(h, ":") {
			return fmt.Errorf("host %q is not a bracketed IPv6 address", h)
		}
		return nil
	}
	if ipv6 && strings.Contains(h, ":") {
		if net.ParseIP(h) == nil {
			return fmt.Errorf("host %q is not an IPv6 address", h)
		}
		return nil
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return fmt.Errorf("host %q has a character a host name cannot", h)
		}
	}
	return nil
}
