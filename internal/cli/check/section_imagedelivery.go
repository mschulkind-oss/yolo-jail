package check

import (
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// deliveryProbeTimeout bounds both subprocesses this section runs. Neither may
// hang a `yolo check`, and a runtime that cannot answer in this window is an
// unproven fact, which reports nothing (image.DeliveryPreflight says why).
const deliveryProbeTimeout = 10 * time.Second

// reportImageDelivery adds the delivery-route line to the Container Image
// section: whether a launch will copy straight into podman's store or from inside
// podman's own user namespace, and — on the rootless host, where it matters — a
// POSITIVE check that the namespace can actually be entered.
//
// WHY THIS IS A PREFLIGHT AND NOT DECORATION. On 2026-09-09 every container job
// in CI failed because the copier could not establish the mapping a rootless
// `containers-storage` needs, and each one discovered that only after building a
// patched skopeo and starting a multi-gigabyte copy. The route is decided from
// `podman info` before anything runs (internal/image/storewrite.go), so it can be
// reported before anything runs too — which is the whole job of this command.
//
// It asks podman TWICE (once for `info`, once for the namespace) and only on the
// host path: in-jail the image is the host's business, and on macOS delivery goes
// through an archive that needs no namespace at all.
func (o *Options) reportImageDelivery(r *reporter, detectedRuntime string) {
	if o.IsMacOS || detectedRuntime != "podman" {
		return
	}
	facts := image.ReadPodmanStoreFacts(detectedRuntime, func(argv []string) (string, bool) {
		res := o.Exec(argv, "", nil, deliveryProbeTimeout)
		if !res.Ran || res.Timeout || res.RC != 0 {
			return "", false
		}
		return res.Stdout, true
	})
	pf := image.UnsharePreflight(detectedRuntime, facts.Rootless, o.PathExists("/bin/sh"),
		func(argv []string) bool {
			res := o.Exec(argv, "", nil, deliveryProbeTimeout)
			return res.Ran && !res.Timeout && res.RC == 0
		})
	// The namespace probe passing says nothing about WHICH store the copy writes
	// (issue #47 passed it and failed every launch), so the store is its own line,
	// from the same `podman info`.
	for _, line := range []image.DeliveryPreflight{pf, image.StorePreflight(facts)} {
		switch {
		case line.Line == "":
		case line.Warn:
			r.warn(line.Line, line.Hint)
		default:
			r.ok(line.Line)
		}
	}
}
