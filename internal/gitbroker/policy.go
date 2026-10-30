package gitbroker

import (
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
)

// policy.go is what covers a pushed ref (docs/design/git-broker.md §5 and §5.1, GB-D12, GB-D13,
// GB-D25, docs/design/git-broker-plan.md ID-9 and ID-11):
//
//   - a creation (old id all zeros) under refs/heads/ is covered by an approved `create-branch`
//     grant on the repository, or on `*`, while the user's own config still lists the kind;
//   - a creation, update or delete under refs/heads/ is covered by the workspace's working set:
//     a live entry in the store for that workspace, repository and ref, or a branch an approved
//     `branch` grant declares in config (by name, or by a pattern matching it, on the
//     repository or on `*`) while the user's own config still lists the kind;
//   - a creation, update or delete under refs/tags/ is covered by an approved `tag` grant whose
//     name or pattern matches the tag, on the repository or on `*`, while the user's own config
//     still lists the kind; a tag never joins the working set;
//   - a ref, tag or branch, a human allowed for a time (`yolo approve <id> --for`) is covered
//     by that timed entry in the store until it expires (GB-D26);
//   - nothing else is covered: any other tag or branch needs approval, which the push asks for
//     (approval.go).
//
// The live scope file's grants and the user config's `grant_kinds` are read at every push, so
// a `yolo config apply` or an edit of the user config on the host reaches the next one. A
// creation under create-branch the upstream then reports `ok` joins the working set in the
// store; a delete of a store entry reported `ok` ends it. A branch declared in config is never
// recorded: the config holds it, so a delete leaves it declared and removing the grant ends it.

const (
	// The grant kinds the git source defines.
	grantCreateBranch = "create-branch"
	grantBranch       = brokerscope.KindBranch
	grantTag          = brokerscope.KindTag
	// The audit sets a covered push falls in (ID-13).
	setCreateBranch = "create-branch"
	setWorkingSet   = "working-set"
	setTimed        = "timed-grant"
	setTag          = "tag"
	// byCreateBranch is a working-set entry's By when a creation under the grant added it.
	byCreateBranch = "create-branch"
)

// KindCapabilities is what packs/git's manifest declares as `brokered.kind_capabilities`: the
// readable capability each grant kind is allowed under (`allow.git.*`) and requested under
// (`requests.git.*`), which the push hints name in their `yolo allow` and `yolo request`
// commands. TestKindCapabilitiesAreTheManifests pins the two together.
var KindCapabilities = map[string]string{
	grantCreateBranch: "new_branches",
	grantBranch:       "push",
	grantTag:          "push_tags",
}

// AllowOnRequestCommand is the user-ceiling command letting a workspace hold a grant of kind:
// `yolo allow git on_request <capability>`, the list a project's request is judged against.
// internal/cli pins that it parses.
func AllowOnRequestCommand(kind string) string {
	return "yolo allow " + Source + " on_request " + KindCapabilities[kind]
}

// RequestCommand is the project command that declares g in the workspace config:
// `yolo request git <capability> [ref]`, or "" for a kind the git source does not define.
// internal/cli pins that it parses.
func RequestCommand(g brokerscope.Grant) string {
	capability := KindCapabilities[g.Kind]
	switch g.Kind {
	case grantBranch:
		return "yolo request " + Source + " " + capability + " " + g.Branch
	case grantTag:
		return "yolo request " + Source + " " + capability + " " + g.Tag
	case grantCreateBranch:
		return "yolo request " + Source + " " + capability
	}
	return ""
}

// allowAdvice is the step that lets the host user config allow kind: the `yolo allow` command,
// behind `yolo config migrate` for a user config still in legacy JSONC, which the verb does not
// write (docs/design/config-commands.md §4).
func allowAdvice(kind string) string {
	allow := "`" + AllowOnRequestCommand(kind) + "`"
	if _, toml := config.UserConfigFile(); !toml {
		return "convert your user config to TOML with `yolo config migrate` on the host, then run " + allow + " there"
	}
	return "run " + allow + " on the host"
}

// coverer decides whether one ref command is covered, and returns the set it falls in and what
// covered it: the grant (`create-branch`; `branch:<name>` for a branch declared in config, or
// `tag:<name>` for a tag a tag grant covers, <name> being the grant's own, pattern or not), or
// the store's working-set entry id.
type coverer func(gitproto.Command) (set, coveredBy string, ok bool)

// grantCovers reports whether grants hold a create-branch grant on repo or on every repository.
func grantCovers(grants []brokerscope.Grant, repo string) bool {
	for _, g := range grants {
		if g.Kind == grantCreateBranch && (g.Repo == brokerscope.AnyRepo || g.Repo == repo) {
			return true
		}
	}
	return false
}

// declaredBy is the approved grant of kind (`branch` or `tag`) covering ref on repo: one naming
// the repository or "*", whose name or pattern matches the ref's short name (GB-D25). A grant
// naming the ref outright is preferred to a pattern, so the audit names the narrowest one.
func declaredBy(grants []brokerscope.Grant, kind, repo, ref string) (brokerscope.Grant, bool) {
	same := func(grantRepo, repo string) bool { return grantRepo == repo }
	var found brokerscope.Grant
	ok := false
	for _, g := range grants {
		if g.Kind != kind || !g.Covers(repo, ref, same) {
			continue
		}
		if !strings.Contains(g.Name(), "*") {
			return g, true
		}
		if !ok {
			found, ok = g, true
		}
	}
	return found, ok
}

// coverFor is the push policy for one push to repo under the live scope file f. A store it
// cannot read covers nothing, and says so once.
func (b *Broker) coverFor(f brokerscope.File, repo string, say func(string)) coverer {
	if b.cover != nil {
		return b.cover
	}
	kinds, canonical, authorityErr := config.BrokeredGrantKindsUsingReadablePolicy(Source)
	granted := grantCovers(f.Grants, repo)
	canCreate := authorityErr == nil && granted && slices.Contains(kinds, grantCreateBranch)
	branchAllowed := authorityErr == nil && slices.Contains(kinds, grantBranch)
	tagAllowed := authorityErr == nil && slices.Contains(kinds, grantTag)
	kindWarned := map[string]bool{}
	kindMissing := func(kind, what string) {
		if !kindWarned[kind] {
			kindWarned[kind] = true
			switch {
			case authorityErr != nil:
				say("this workspace's " + kind + " grant on " + repo + " is not in effect: the host user config's canonical `allow.git` write authority could not be validated, so the broker covers no such write. Repair `allow.git.always` or `allow.git.on_request` on the host, then push again; the broker rereads that config on each push, or push from the host.")
			case canonical:
				say("this workspace's " + kind + " grant on " + repo + " is not in effect: the host user config's canonical `allow.git` does not authorize it under the selected broker's declared capabilities. To let the broker " + what + ", " + allowAdvice(kind) + ", then push again; the broker rereads that config on each push, or push from the host.")
			default:
				say("this workspace's " + kind + " grant on " + repo + " is not in effect: your user config does not " +
					"allow " + KindCapabilities[kind] + " under `allow.git`. To let the broker " + what + ", " +
					allowAdvice(kind) + " (the next push reads it), or push from the host.")
			}
		}
	}
	// unlisted is what a grant of kind also needs from the host user config before it covers
	// anything, as the hint's clause; nothing when the config already allows the kind.
	unlisted := func(kind string) string {
		switch {
		case authorityErr != nil:
			return ", and repair `allow.git` in your user config on the host, which could not be validated"
		case slices.Contains(kinds, kind):
			return ""
		default:
			return ", and " + allowAdvice(kind) + " to allow it in your user config"
		}
	}
	// hint is a refused ref's next step: the verb that declares the one grant covering it in the
	// workspace config, behind `yolo config migrate` for a project still on yolo-jail.jsonc,
	// which the verb does not write (docs/reference/happy-path-principle.md,
	// docs/design/config-commands.md §4). The hint names no legacy key to hand-edit.
	hint := func(ref, why string, g brokerscope.Grant) {
		say(ref + ": " + why + ". To let the broker push it unasked, run `" + RequestCommand(g) +
			"` in this workspace (a project still on yolo-jail.jsonc converts first with `yolo config migrate`)" +
			unlisted(g.Kind) + ", then run `yolo config apply` on the host, in this workspace, and approve the " +
			"grant; or push it from the host (`git push` in this workspace there).")
	}
	warned := false
	storeFailed := func(err error) {
		if !warned {
			warned = true
			say("the git-broker could not read this workspace's working set (" + err.Error() + "), so no branch " +
				"in it is covered for this push. Check that the host store under " + b.store.Dir + " is readable, " +
				"then push again.")
		}
	}
	return func(c gitproto.Command) (string, string, bool) {
		if tag, isTag := strings.CutPrefix(c.Ref, "refs/tags/"); isTag {
			// A tag a tag grant covers, its kind allowed, is pushed unasked: creation, update,
			// force and delete alike (GB-D25). It never joins the working set.
			g, declared := declaredBy(f.Grants, grantTag, repo, c.Ref)
			const asks = "so this push asks for approval (`yolo approve` on the host answers it: Allow once, `--for 15m`, or `--add` for a branch)"
			switch {
			case declared && tagAllowed:
				return setTag, grantTag + ":" + g.Name(), true
			case declared:
				kindMissing(grantTag, "push the tags it names")
			}
			// A tag a human allowed for a time (GB-D26): a timed entry, never a lasting one.
			if e, live, err := b.store.LiveWorking(b.bound.Workspace, repo, c.Ref); err != nil {
				storeFailed(err)
			} else if live {
				return setTimed, e.ID, true
			}
			switch {
			case declared:
			case brokerscope.ValidGrantBranch(tag):
				hint(c.Ref, "no tag grant covers it, "+asks, brokerscope.Grant{Kind: grantTag, Repo: repo, Tag: tag})
			default:
				say(c.Ref + ": no tag grant covers it and none can name it, " + asks + "; or push it from the " +
					"host (`git push` in this workspace there).")
			}
			return "", "", false
		}
		branch, isBranch := strings.CutPrefix(c.Ref, "refs/heads/")
		if !isBranch {
			return "", "", false
		}
		dg, declared := declaredBy(f.Grants, grantBranch, repo, c.Ref)
		// A branch declared in config, its kind allowed, is in the working set: creation,
		// update, force and delete alike (GB-D13), by name or by pattern (GB-D25).
		inConfig := func() (string, string, bool) {
			if !declared || !branchAllowed {
				return "", "", false
			}
			return setWorkingSet, grantBranch + ":" + dg.Name(), true
		}
		// refused is the end of every path that covers nothing: a declared branch whose kind
		// the user config no longer lists says so only here, once the push is refused, so a
		// push another grant covers prints no refusal line.
		refused := func() (string, string, bool) {
			switch {
			case declared && !branchAllowed:
				kindMissing(grantBranch, "push to the branches it declares")
			case c.Create() && granted:
				// kindMissing already named what keeps the create-branch grant from covering it.
			case c.Create():
				hint(c.Ref, "a new branch needs a create-branch grant on "+repo+", which covers any new branch name",
					brokerscope.Grant{Kind: grantCreateBranch, Repo: repo})
			case brokerscope.ValidGrantBranch(branch):
				hint(c.Ref, "not in this workspace's working set: the broker did not create it, and no branch grant declares it",
					brokerscope.Grant{Kind: grantBranch, Repo: repo, Branch: branch})
			default:
				say(c.Ref + ": not in this workspace's working set, and no branch grant can name it; push it from the host " +
					"(`git push` in this workspace there).")
			}
			return "", "", false
		}
		orRefused := func(s, by string, ok bool) (string, string, bool) {
			if ok {
				return s, by, ok
			}
			return refused()
		}
		e, live, err := b.store.LiveWorking(b.bound.Workspace, repo, c.Ref)
		if err != nil {
			storeFailed(err)
			// The store is not needed for a branch the config declares.
			return orRefused(inConfig())
		}
		if live && e.Timed() {
			// Allowed for a time (GB-D26): a creation, update or delete alike. A creation the
			// upstream then accepts makes the entry lasting (record).
			return setTimed, e.ID, true
		}
		if c.Create() {
			// The workspace held this branch, and the upstream no longer has it: someone
			// deleted it outside the broker. Its entry ends, and the push is a creation.
			if live {
				if err := b.store.EndWorking(e.ID, brokerstore.EndGone); err != nil {
					storeFailed(err)
					return orRefused(inConfig())
				}
			}
			// Declared in config first: such a branch is never recorded in the store.
			if s, by, ok := inConfig(); ok {
				return s, by, ok
			}
			if canCreate {
				return setCreateBranch, grantCreateBranch, true
			}
			if granted {
				kindMissing(grantCreateBranch, "create branches")
			}
			return refused()
		}
		// A store entry first, so a delete through the broker ends the record it covers.
		if live {
			return setWorkingSet, e.ID, true
		}
		return orRefused(inConfig())
	}
}

// errNoReport is a forwarded push whose client asked for no report-status, so nothing says
// which refs the upstream took.
var errNoReport = errors.New("the push asked for no report")

// relayReport copies the upstream's answer to a forwarded push to the client, and reads
// receive-pack's report out of the same bytes. Every byte is read from the upstream even once
// the client stops reading, so the report is still known.
func relayReport(out io.Writer, upstream io.Reader, caps gitproto.Caps) (gitproto.Report, error) {
	if !caps.Has("report-status") && !caps.Has("report-status-v2") {
		_, _ = io.Copy(out, upstream)
		return gitproto.Report{}, errNoReport
	}
	type result struct {
		rep gitproto.Report
		err error
	}
	pr, pw := io.Pipe()
	done := make(chan result, 1)
	go func() {
		rep, err := gitproto.ReadReport(gitproto.NewReader(pr), gitproto.UsesSideband(caps))
		_, _ = io.Copy(io.Discard, pr)
		done <- result{rep, err}
	}()
	buf := make([]byte, 32<<10)
	clientGone := false
	for {
		n, err := upstream.Read(buf)
		if n > 0 {
			_, _ = pw.Write(buf[:n])
			if !clientGone {
				if _, werr := out.Write(buf[:n]); werr != nil {
					clientGone = true
				}
			}
		}
		if err != nil {
			break
		}
	}
	_ = pw.Close()
	r := <-done
	return r.rep, r.err
}

// record updates the working set from the upstream's report of a forwarded push: a branch
// creation covered by the create-branch grant or by an Allow once and reported `ok` joins it,
// by that grant (GB-D22), and a delete of an entry reported `ok` ends it. A creation the report
// does not confirm records nothing (§5.1: a creation the push lost); a tag never joins.
func (b *Broker) record(repo string, p pushRead, refs []brokeraudit.RefUpdate, rep gitproto.Report, repErr error, say func(string)) {
	for i, c := range p.cmds {
		by := refs[i].CoveredBy
		// A creation covered by a w- entry was covered by a timed one (GB-D26): the branch the
		// agent then created is the workspace's, so the entry becomes lasting, by itself.
		timedCreate := c.Create() && strings.HasPrefix(refs[i].CoveredBy, "w-")
		if !strings.HasPrefix(by, "g-") && !timedCreate {
			by = byCreateBranch
		}
		created := c.Create() && strings.HasPrefix(c.Ref, "refs/heads/") &&
			(refs[i].CoveredBy == grantCreateBranch || strings.HasPrefix(refs[i].CoveredBy, "g-") || timedCreate)
		deleted := c.Delete() && strings.HasPrefix(refs[i].CoveredBy, "w-")
		if !created && !deleted {
			continue
		}
		if repErr != nil {
			say("the remote's report could not be read (" + repErr.Error() + "), so " + c.Ref + " was not " +
				"recorded in this workspace's working set; if the remote took it, the next push to it needs approval.")
			continue
		}
		if !rep.OK(c.Ref) {
			continue
		}
		var err error
		if created {
			_, _, err = b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.bound.Workspace, Repo: repo,
				Kind: brokerstore.WorkingBranch, Resource: c.Ref, By: by})
		} else {
			err = b.store.EndWorking(refs[i].CoveredBy, brokerstore.EndDeleted)
		}
		if err != nil {
			say("the remote took " + c.Ref + ", but the git-broker could not record it in this workspace's " +
				"working set (" + err.Error() + "); `yolo approve` on the host lists the working set.")
		}
	}
}
