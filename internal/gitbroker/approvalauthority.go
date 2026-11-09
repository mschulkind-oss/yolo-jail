package gitbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// approvalauthority.go is the host's authority over a push that asks for approval
// (docs/design/git-broker.md §5, GB-D8 and GB-D16 to GB-D23): what a request for one is, and
// the check every step of its life re-runs against the launch's live scope file: filing it,
// deciding it with `yolo approve`, and spending its grant.
//
// A push's request is the whole push (GB-D4, GB-D17): its argv is `git-receive-pack`, one
// "<old> <new> <ref>" per ref command, and one "push-option=<value>" per push option the push
// sends upstream (a server acts on them: GitLab merges or sets CI variables by them), its repository is the remote's identity
// (gitproto.Identity), and its stdin digest is that of nothing, since the new ids already name
// what the push sends. The answers are Allow once, Deny, Allow push to the refs for a time and
// Add the refs to the working set (GB-D16, GB-D26): Allow once is one use of that exact push,
// held by the workspace, for 15 minutes; the other two are entries per ref, in the working-set
// store (pushEntries).

// setPush is the request set of a push that asked for approval, and the audit set a push an
// Allow once covered falls in.
const setPush = "push"

// pushPolicy is every push request's fixed policy: Allow once is one use for 15 minutes
// (GB-D16), and `yolo approve --for <duration>` may run as long as MaxWindow (GB-D26).
var pushPolicy = brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: 24 * time.Hour, Uses: 1}

// pushStdinDigest is a push request's stdin digest: SHA-256 of nothing.
var pushStdinDigest = func() string {
	sum := sha256.Sum256(nil)
	return hex.EncodeToString(sum[:])
}()

// pushOptionPrefix starts a push option's line in a push request's argv. A ref command's line
// starts with an object id, so the two never collide.
const pushOptionPrefix = "push-option="

// pushArgv is the canonical command of a push: `git-receive-pack`, then each ref command as
// "<old> <new> <ref>", then each push option as "push-option=<value>", each in the order the
// push sent them.
func pushArgv(cmds []gitproto.Command, opts []string) []string {
	argv := []string{serviceReceivePack}
	for _, c := range cmds {
		argv = append(argv, c.Old+" "+c.New+" "+c.Ref)
	}
	for _, o := range opts {
		argv = append(argv, pushOptionPrefix+o)
	}
	return argv
}

// checkPushArgv reports why argv is not a push request's command, or nil.
func checkPushArgv(argv []string) error {
	if len(argv) < 2 || argv[0] != serviceReceivePack {
		return errors.New("the request is not a push of at least one ref")
	}
	options := false
	for i, line := range argv[1:] {
		if o, ok := strings.CutPrefix(line, pushOptionPrefix); ok {
			if i == 0 || strings.ContainsAny(o, "\n\x00") {
				return errors.New("the request holds a malformed push option")
			}
			options = true
			continue
		}
		if options {
			return errors.New("the request holds a ref command after its push options")
		}
		f := strings.Split(line, " ")
		if len(f) != 3 || !objectID(f[0]) || !objectID(f[1]) {
			return errors.New("the request holds a malformed ref command")
		}
		if !strings.HasPrefix(f[2], "refs/heads/") && !strings.HasPrefix(f[2], "refs/tags/") {
			return errors.New("the request names a ref outside refs/heads/ and refs/tags/, which no approval covers")
		}
	}
	return nil
}

// objectID reports whether s is an object id as git writes one.
func objectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// holderJail is the jail a launch's requests are filed under: the short hash of the container
// its scope file names. The broker serves that one jail, so the holder comes from the host's
// scope file, never from the connection.
func holderJail(sf brokerscope.File) string { return paths.JailShortHash(sf.Container) }

// revalidatePush is the push policy's revalidation of q against the scope file at path, bound
// as bound, while the broker lifetime start is live: the holder is that launch, the command is
// a push of refs under refs/heads/ and refs/tags/, the live scope still approves a remote of
// q's repository, and a read grant still covers it (BB-D108). It returns the fixed policy.
func revalidatePush(path string, bound brokerscope.File, start string, q brokerstore.Request) (brokerstore.Policy, error) {
	if q.Start != start || q.LaunchID != bound.LaunchID || q.Workspace != bound.Workspace || q.Jail != holderJail(bound) {
		return brokerstore.Policy{}, errors.New("request holder is not this live host launch")
	}
	if q.Set != setPush || q.StdinDigest != pushStdinDigest {
		return brokerstore.Policy{}, errors.New("the request is not a git push request")
	}
	if err := checkPushArgv(q.Argv); err != nil {
		return brokerstore.Policy{}, err
	}
	f, err := brokercore.Live(Source, path, bound, start)
	if err != nil {
		return brokerstore.Policy{}, err
	}
	if f.Form != brokerscope.FormRemoteURL {
		return brokerstore.Policy{}, errors.New("scope file is not a remote-url scope")
	}
	if !repoInScope(f, q.Repo) {
		return brokerstore.Policy{}, fmt.Errorf("%s is no longer in this workspace's approved scope", q.Repo)
	}
	if !brokerscope.HoldsRead(f.Grants) || !brokerscope.ReadCovers(f.Grants, q.Repo, func(g, r string) bool { return g == r }) {
		return brokerstore.Policy{}, errors.New(config.BrokerNoReadRefusal(Source, ReadCapability, q.Repo, f.LiveScope))
	}
	return pushPolicy, nil
}

// repoInScope reports whether a remote the scope approves has identity repo.
func repoInScope(f brokerscope.File, repo string) bool {
	for _, approved := range f.Repos {
		if id, err := gitproto.Identity(approved); err == nil && id == repo {
			return true
		}
	}
	return false
}

// ApprovalAuthority is the git store's revalidation for a host front end (`yolo approve`): it
// derives the scope file from the request's validated launch id, never from a path a caller
// gives, and confirms the request only while the broker lifetime that filed it is live.
func ApprovalAuthority(q brokerstore.Request) (brokerstore.Policy, error) {
	if q.LaunchID == "" || strings.ContainsAny(q.LaunchID, "/\\.") {
		return brokerstore.Policy{}, errors.New("request has no valid host launch identity")
	}
	path := paths.BrokerScopeFile(Source, q.LaunchID)
	f, err := brokerscope.ReadFile(path)
	if err != nil {
		return brokerstore.Policy{}, err
	}
	return revalidatePush(path, f, q.Start, q)
}

// StoreAudit is the git store's required audit hook (brokercore.StoreAudit).
func StoreAudit(log *brokeraudit.Log) func(context.Context, string, brokerstore.Request, brokerstore.Grant) error {
	return brokercore.StoreAudit(log, Source)
}

// ApprovalStore is the git source's host store as a front end opens it: its revalidation is
// ApprovalAuthority, its per-ref answers are pushEntries, and every request, decision and spent
// grant is audited.
func ApprovalStore() *brokerstore.Store {
	return &brokerstore.Store{Dir: paths.BrokerSourceDir(Source), Source: Source, Revalidate: ApprovalAuthority, PublishRecords: true,
		Entries: pushEntries, Audit: StoreAudit(brokeraudit.Open(paths.BrokerAuditLog(), nil))}
}

// pushEntries is the timed working-set entries `yolo approve <id> --for <duration>` records for
// push request q: one per ref the request needed approval for (every ref when the request does
// not say), in q's workspace and repository (GB-D26). A tag's entry is of the tag kind.
func pushEntries(q brokerstore.Request) ([]brokerstore.WorkingEntry, error) {
	if err := checkPushArgv(q.Argv); err != nil {
		return nil, err
	}
	needed := map[string]bool{}
	for _, r := range q.NeedsApproval {
		needed[r] = true
	}
	var out []brokerstore.WorkingEntry
	for _, line := range q.Argv[1:] {
		if strings.HasPrefix(line, pushOptionPrefix) {
			continue
		}
		f := strings.Split(line, " ")
		c := gitproto.Command{Old: f[0], New: f[1], Ref: f[2]}
		if len(needed) > 0 && !needed[c.Ref] {
			continue
		}
		e := brokerstore.WorkingEntry{Workspace: q.Workspace, Repo: q.Repo, Kind: brokerstore.WorkingBranch, Resource: c.Ref, By: "human"}
		if strings.HasPrefix(c.Ref, "refs/tags/") {
			e.Kind = brokerstore.WorkingTag
		}
		out = append(out, e)
	}
	return out, nil
}

// pushRequest is the request a push of cmds to repo files from this broker, unfiled.
func (b *Broker) pushRequest(repo string, cmds []gitproto.Command, opts []string) brokerstore.Request {
	q := brokerstore.Request{Jail: holderJail(b.bound), Start: b.start, Workspace: b.bound.Workspace,
		LaunchID: b.bound.LaunchID, Argv: pushArgv(cmds, opts), Repo: repo, Set: setPush, Policy: pushPolicy,
		StdinDigest: pushStdinDigest}
	q.Digest = brokerstore.CommandDigest(q)
	return q
}
