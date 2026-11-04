package check

import (
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// networkFindings is the launch's network refusal predicted (run.NetworkRefusal; named-setups.md
// SU-R5, plan slice 4b): a written `network.mode` the selected setup's technology cannot hold, in
// the launch's own words, as at most one Merged Configuration error. check has no `--network`
// flag, so only the written key is judged. A selection that refuses is its own finding there,
// so it adds none here; nor does a host setup, which only `yolo host` runs and which never reads
// `network.mode` (resolveRuntimeForCheck skips it the same way).
func (o *Options) networkFindings(merged *jsonx.OrderedMap, src *config.Sources) []string {
	choice, err := o.checkSetup(merged)
	if err != nil || choice.Setup.Kind() == setup.KindHost {
		return nil
	}
	here := setup.Here{GOOS: o.checkGOOS(), InSession: o.inJail()}
	if text := run.NetworkRefusal(choice.Setup, merged, "", src, here); text != "" {
		return []string{text}
	}
	return nil
}
