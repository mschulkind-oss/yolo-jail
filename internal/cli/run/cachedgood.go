package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// CachedGoodEnv selects one patched program's exact recorded good build for one fresh jail launch.
const CachedGoodEnv = "YOLO_USE_CACHED_GOOD"

// selectCachedGood validates the launch-only selector against the final selected fork set. It does
// not grant a build, host-floor, attach, or in-jail capability.
func (o *Options) selectCachedGood(rt string) bool {
	selector := o.Getenv(CachedGoodEnv)
	if selector == "" {
		return true
	}
	refuse := func(why string) bool {
		o.pr(o.Stderr).printf("[bold red]Refusing cached-good recovery: %s.[/bold red]", why)
		return false
	}
	if strings.TrimSpace(selector) != selector || strings.ContainsAny(selector, " \t\r\n,") {
		return refuse(CachedGoodEnv + " must name exactly one selected patched program owner key such as `pi-fork/pi`")
	}
	if o.inJail() || config.InJail() {
		return refuse("it applies only to a fresh host-to-container launch")
	}
	if rt != "podman" && rt != "container" { // parity: Refused — macos-user has no capture-store bind to deliver an old build through
		return refuse("it applies only to a fresh podman or Apple Container launch")
	}
	if o.CapturesDir() == "" {
		return refuse("this launch has no host capture store to deliver from")
	}
	if why := o.roBindsUnsupported(rt); why != "" {
		return refuse("this runtime cannot mount the capture store read-only: " + why)
	}
	matches := 0
	for _, pin := range o.forkPinned {
		if pin.Fork.Key() == selector && pin.Fork.Patched() {
			matches++
		}
	}
	if matches != 1 {
		return refuse("" + selector + " is not exactly one selected patched program; extension owners, unselected packs, and other forks are not eligible")
	}
	o.cachedGoodOwner = selector
	return true
}
