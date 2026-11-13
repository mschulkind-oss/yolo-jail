package run

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/selfupdate"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// applybrokerskew.go is `yolo config apply`'s note that this workspace's host brokers were
// started by an OLDER yolo than the one applying.
//
// WHY. Apply compares the workspace config with what the jail was handed, and replaces the live
// scope files; it never asks a broker how it reads the user config. A running broker rereads that
// config at every call as the yolo that started it reads it, so a grant apply shows in force, or
// has just applied, can still be refused there: on 2026-11-09 a jail launched by an older yolo
// refused its `branch:main` grant after a newer `yolo allow` wrote a capability name the older
// one does not declare, while apply reported nothing to apply.
//
// ONLY WHEN THIS YOLO IS NEWER. A newer yolo reads what an older one writes (its former
// spellings, internal/config/formerspellings.go), so the exposed direction is an older broker
// under a newer host yolo, and a relaunch onto an older host yolo is never advised. The note is
// definite: a launch pins its helpers to the yolo it started with and a restart never runs another
// build (docs/reference/pinned-host-helpers.md "What stays unpinned", PH-D10), so the brokers do run the older one.

// noteBrokersStartedByAnOlderYolo prints the note, or nothing: silent unless the yolo that
// started this workspace's brokers is known and this one is provably newer.
func (o *Options) noteBrokersStartedByAnOlderYolo(out printer, rt, cname string, envLines []string) {
	started := o.brokersStartedBy(rt, cname, envLines)
	host := o.yoloVersion("")
	if started == "" || host == "" || host == "unknown" || !yoloVersionNewer(host, started) {
		return
	}
	out.print("[dim]" + richtext.Escape(termsafe.Visible(fmt.Sprintf(
		"This workspace's host brokers run yolo %s, the build that started them; this is yolo %s. "+
			"A broker rereads your user config at every call as the yolo it runs reads it, so a grant "+
			"in force here can still be refused there. To run them on this yolo, %s once its sessions "+
			"are done, then launch again.", started, host, keyedStopRemedy(rt, cname)))) + "[/dim]")
}

// brokersStartedBy is the version of the yolo that started this workspace's host brokers, or "".
// A container jail's brokers are started by the launch that started the container, whose version
// the container's environment carries (YOLO_VERSION). A macos-user workspace's are held by its
// keyed keeper (keeperspawn.go, JL-D45), whose live start record names its build.
func (o *Options) brokersStartedBy(rt, cname string, envLines []string) string {
	if rt != "macos-user" { // parity: HonoredBy — macos-user's brokers are its keeper's, read below; a container's are its launch's
		v, _ := runtime.BakedYoloVersionFromInspectEnv(envLines)
		return v
	}
	for _, notch := range macosUserKeeperNotches {
		rec, ok := readKeeperRecord(keeperKey(cname, notch))
		if !ok || rec.Ending || rec.Build == "" || (o.PIDAlive != nil && !o.PIDAlive(rec.PID)) {
			continue
		}
		v, _, _ := strings.Cut(rec.Build, "@") // keeperBuildStamp: version@commit
		return v
	}
	return ""
}

// yoloVersionNewer reports whether yolo version a is provably newer than b: a newer
// MAJOR.MINOR.PATCH (selfupdate.Newer), or, on the same one, a larger commit distance
// ("0.16.0+87.g…" is 87 commits past 0.16.0). Anything it cannot order is not newer.
func yoloVersionNewer(a, b string) bool {
	if selfupdate.Newer(a, b) {
		return true
	}
	if !selfupdate.AtLeast(a, b) { // b is newer, or either does not parse
		return false
	}
	da, aok := yoloCommitDistance(a)
	db, bok := yoloCommitDistance(b)
	return aok && bok && da > db
}

// yoloCommitDistance is the commit distance in a normalized version (version.Normalize): the
// leading number of its "+" metadata, 0 for a version exactly on its tag or one whose metadata
// carries none ("0.1.0+gabcdef1").
func yoloCommitDistance(v string) (int, bool) {
	_, meta, found := strings.Cut(v, "+")
	if !found {
		return 0, true
	}
	head, _, _ := strings.Cut(meta, ".")
	if n, err := strconv.Atoi(head); err == nil {
		return n, true
	}
	return 0, true
}
