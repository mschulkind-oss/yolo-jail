package awsauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// modelcache.go is the BEDROCK LIST CACHE: every list modellist.go fetched, kept for a day per
// account and region (docs/design/model-lists-and-pickers.md OQ-MM6), in the aws-auth state
// directory beside the minted-credential cache and with its discipline — mode 0600 in a 0700
// directory, atomic replacement, never mounted into a jail. It holds model ids, makers and names,
// and an index from each profile that fetched to the account it resolved to, so a launch that
// knows only its profile finds the account's lists with no `aws` call.
//
// ONE READ-MODIFY-WRITE AT A TIME, under its own lock file: the daemon writes it, and so does a
// launch that found no daemon to ask (internal/cli/run's bedrockmodels.go), and two regions
// fetched at once must not drop each other's entry. A reader takes no lock: the rename means it
// sees one whole generation or the next.

// ModelCacheFileName is the cache's file in the aws-auth state directory.
const ModelCacheFileName = "bedrock-models.json"

// modelCacheLockName is the lock file a writer holds, beside the cache.
const modelCacheLockName = "bedrock-models.lock"

const modelCacheVersion = 1

// ModelCache is the whole cache.
type ModelCache struct {
	Version int `json:"version"`
	// Profiles is profile name -> the account its credentials resolved to when it last fetched.
	Profiles map[string]string `json:"profiles"`
	// Lists is account -> region -> that region's list.
	Lists map[string]map[string]ModelList `json:"lists"`
}

// ModelCachePath is the cache beside a credential state file.
func ModelCachePath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), ModelCacheFileName)
}

// LoadModelCache reads the cache. An absent, unreadable or unparseable file is an EMPTY cache and
// the error, for the reason loadState gives: the cache only saves a fetch, so a corrupt one costs
// one fetch and must never stop a launch.
func LoadModelCache(path string) (ModelCache, error) {
	empty := ModelCache{Version: modelCacheVersion, Profiles: map[string]string{},
		Lists: map[string]map[string]ModelList{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty, err
	}
	var c ModelCache
	if err := json.Unmarshal(data, &c); err != nil {
		return empty, fmt.Errorf("decode the Bedrock model list cache: %w", err)
	}
	if c.Version != modelCacheVersion {
		return empty, fmt.Errorf("unsupported Bedrock model list cache version %d", c.Version)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]string{}
	}
	if c.Lists == nil {
		c.Lists = map[string]map[string]ModelList{}
	}
	return c, nil
}

// Lookup is the list profile's account last had in region, found through the profile index.
func (c ModelCache) Lookup(profile, region string) (ModelList, bool) {
	account := c.Profiles[profile]
	if account == "" {
		return ModelList{}, false
	}
	l, ok := c.Lists[account][region]
	return l, ok && l.FetchedAtMS > 0
}

// StoreModelList records list, fetched as profile, in the cache at path: read, change, write,
// under the cache's lock. create says whether a missing state directory is made (the daemon's
// NoCreateDir rule, ensureStateDir).
func StoreModelList(path, profile string, list ModelList, create bool) error {
	if list.Account == "" || list.Region == "" {
		return errors.New("a Bedrock model list with no account or region is not cached")
	}
	dir := filepath.Dir(path)
	if err := ensureStateDir(dir, create); err != nil {
		return err
	}
	unlock, err := lockModelCache(filepath.Join(dir, modelCacheLockName))
	if err != nil {
		return err
	}
	defer unlock()
	c, _ := LoadModelCache(path) // a corrupt cache is replaced, as an empty one is filled
	c.Profiles[profile] = list.Account
	if c.Lists[list.Account] == nil {
		c.Lists[list.Account] = map[string]ModelList{}
	}
	c.Lists[list.Account][list.Region] = list
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the Bedrock model list cache: %w", err)
	}
	return replaceFile(dir, path, append(data, '\n'))
}

// lockModelCache takes the writers' lock, blocking.
func lockModelCache(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the Bedrock model list cache lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock the Bedrock model list cache: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// replaceFile writes data to path through a 0600 temporary file in dir and a rename.
func replaceFile(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".bedrock-models.*")
	if err != nil {
		return fmt.Errorf("create a temporary Bedrock model list cache: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure the temporary Bedrock model list cache: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the temporary Bedrock model list cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close the temporary Bedrock model list cache: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace the Bedrock model list cache: %w", err)
	}
	return nil
}
