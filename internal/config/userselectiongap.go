package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// UserScopeSelectionGap says, in one sentence with its next step, why the user's pack selection
// does not resolve completely: the first entry that does not (an unfetched remote, a vanished
// local directory, a declaration that does not read) with its own error, or the selection
// closure's refusal. It reads and never fetches, so it can name the gap without changing it. ""
// means the selection resolves.
func UserScopeSelectionGap() string {
	entries, err := LoadPacks(func(string) {})
	if err != nil {
		return "the user-scope pack list cannot be read (" + oneLineErr(err) + "); run `yolo config check` and fix what it names"
	}
	resolved := SelectedPackDeclarationsDetailed(entries, UserScopeSelection(), nil, packload.EmbeddedFS())
	embeddedNames := packload.EmbeddedNames()
	for _, u := range resolved.Unresolved {
		reason := "its declaration does not read: " + oneLineErr(u.Err)
		next := "check it with `yolo pack ls`, or drop it with `yolo pack remove " + u.Entry.Name + "`"
		if missing, source := vanishedLocalPack(u.Entry, embeddedNames); missing {
			reason = "its local source " + source + " does not exist"
			next = "restore that directory or drop the pack with `yolo pack remove " + u.Entry.Name + "`"
		} else if _, fetchable, ok := skippableMissingPack(u.Entry, u.Err, nil, entries, embeddedNames); ok && fetchable {
			reason = "it has not been fetched into the pack store yet"
			next = "fetch it with `yolo pack install` on the host, then retry the launch"
		}
		return "selected pack " + u.Entry.Name + " was not read (" + reason + "); " + next
	}
	if resolved.ClosureErr != nil {
		return "the pack selection does not close (" + oneLineErr(resolved.ClosureErr) + "); fix it as that message says, then retry the launch"
	}
	return ""
}
