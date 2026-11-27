package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// toolskills.go is the host half of mise tool skills (docs/reference/mise-tool-skills.md, slice 1):
// the ALLOWLIST of tools whose skills a launch may deliver, and the SNAPSHOT TOKEN guarding the
// snapshot a jail writes (MS-D1, MS-D9).

// MiseSkillTools is the allowlist of mise tools whose skills may reach a jail (MS-D1): the
// tool names of the USER scope's own `install.mise` table (OwnMiseTools, so the legacy
// `mise_tools` spelling counts), in declaration order, with every `off` value left out. user
// is the user scope alone, never the merged config: a workspace may override a listed tool's
// version but never adds a tool to the list. It is empty for a sealed or capture jail (they
// withhold the user's tools) and for a launch run from inside a jail, whose "user scope" is the
// outer jail's effective config and so is not the user's.
func MiseSkillTools(user *jsonx.OrderedMap, sealed, captureJail, nested bool) []string {
	out := []string{}
	if sealed || captureJail || nested {
		return out
	}
	own := OwnMiseTools(user)
	if own == nil {
		return out
	}
	for _, k := range own.Keys() {
		if v, _ := own.Get(k); v == "off" {
			continue
		}
		out = append(out, k)
	}
	return out
}

// toolSkillsTokensLeaf is the folder of snapshot tokens under yolo's global storage. It is not
// under cache/, which every jail mounts read-write, and not under any workspace.
const toolSkillsTokensLeaf = "tool-skills-tokens"

// ToolSkillsToken is the workspace's SNAPSHOT TOKEN (coined in the design): 32 random bytes, hex,
// minted once on the host the first time it is asked for and read back unchanged on every later
// launch. It lives in yolo's own host storage, named by the resolved workspace as the
// per-workspace file is (WorkspaceFilePath), mode 0600 in a 0700 folder, and never under the
// workspace, where a repository could ship one. A token file that is not a regular file, or
// whose content is not a token, is replaced, never followed.
func ToolSkillsToken(workspace string) (string, error) {
	resolved := resolveWorkspaceForFile(workspace)
	sum := sha256.Sum256([]byte(resolved))
	dir := filepath.Join(paths.GlobalStorage(), toolSkillsTokensLeaf)
	path := filepath.Join(dir, hex.EncodeToString(sum[:])[:16]+".token")
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() {
		if b, err := os.ReadFile(path); err == nil {
			if t := strings.TrimSpace(string(b)); validToolSkillsToken(t) {
				return t, nil
			}
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := writePrivateAtomically(path, []byte(token+"\n")); err != nil {
		return "", err
	}
	return token, nil
}

func validToolSkillsToken(t string) bool {
	if len(t) != 64 {
		return false
	}
	_, err := hex.DecodeString(t)
	return err == nil
}
