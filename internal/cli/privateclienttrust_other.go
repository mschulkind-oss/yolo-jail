//go:build !linux

package cli

import "errors"

// privateClientTrustPath has no private vehicle off Linux: a private client runs only in a Podman
// jail, and any other cell is refused before it is prepared.
func privateClientTrustPath([]byte) (string, error) {
	return "", errors.New("a published private client's trust can be delivered only in a Linux jail")
}
