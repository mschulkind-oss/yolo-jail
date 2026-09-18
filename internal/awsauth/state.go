package awsauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// state.go is the minted-credential cache: ONE writer (the host service, under
// the host-wide lock in broker.go), atomic replacement, 0600 in a 0700 directory.
//
// # Keyed by profile, and the map is the ruling rather than a convenience
//
// OQ-SSO2 ruled the service a HOST SINGLETON with its cache keyed BY PROFILE, so
// that one process can still serve several AWS identities rather than becoming one
// AWS identity per machine. Today the settings file names one profile, so the map
// holds one entry — the map is what makes a second identity a configuration change
// rather than a state-format change.
//
// # The state file never crosses into a jail
//
// Nothing here is mounted. A loophole's state dir crosses into the container only
// for a manifest with a `jail_daemon`, and then only the files named in
// `state_files` — the manifest names an inert `.mount-sentinel` and nothing else,
// which is the discipline internal/openaiauth records as "keep this list nonempty or
// credentials.json would cross into every jail." The mode is the host-side,
// single-user half of the story only: a jail's agent runs as UID 0, so a file the
// container can see at all it can read.

const stateVersion = 1

// State is the whole cache.
type State struct {
	Version int `json:"version"`
	// Credentials is profile name -> the credential last minted for it.
	Credentials map[string]Credential `json:"credentials"`
}

func (s State) validate() error {
	if s.Version != stateVersion {
		return fmt.Errorf("unsupported aws-auth credential state version %d", s.Version)
	}
	return nil
}

// loadState returns the cache, or an EMPTY one for a file that is absent,
// unreadable or unparseable.
//
// AN UNREADABLE CACHE IS A CACHE MISS, not an error, and that is the fail-forward
// direction rather than a shortcut: the only thing in here is a credential that can
// be re-minted from the host's live SSO session in one shell-out. Refusing to serve
// because a cache file is corrupt would turn a self-healing condition into an outage.
// The caller is told, so the corruption is not silent.
func loadState(path string) (State, error) {
	empty := State{Version: stateVersion, Credentials: map[string]Credential{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return empty, fmt.Errorf("decode aws-auth credential state: %w", err)
	}
	if err := state.validate(); err != nil {
		return empty, err
	}
	if state.Credentials == nil {
		state.Credentials = map[string]Credential{}
	}
	return state, nil
}

// writeState atomically replaces the cache with a mode-0600 file in a mode-0700
// directory. The rename means a lock-free reader — the warm-cache serve path — sees
// the old complete generation or the new complete one, never a torn file.
func writeState(path string, state State) error {
	if state.Version == 0 {
		state.Version = stateVersion
	}
	if err := state.validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create aws-auth credential directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure aws-auth credential directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode aws-auth credential state: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".aws-auth.*")
	if err != nil {
		return fmt.Errorf("create temporary aws-auth credential state: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary aws-auth credential state: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary aws-auth credential state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary aws-auth credential state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary aws-auth credential state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace aws-auth credential state: %w", err)
	}
	return nil
}
