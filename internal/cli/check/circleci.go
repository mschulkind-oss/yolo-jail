package check

import "github.com/mschulkind-oss/yolo-jail/internal/packload"

// reportSelectedCallBrokerPrerequisites checks the declared host executable only.
// It never calls a service, reads credential files, inspects keychains or claims login.
func (o *Options) reportSelectedCallBrokerPrerequisites(r *reporter, packs []*packload.Pack) {
	for _, pack := range packs {
		if pack == nil || pack.Name != "circleci" {
			continue
		}
		modules, _, _ := pack.LoopholeModules()
		for _, module := range modules {
			if module.Decl == nil || module.Decl.CallBroker == nil || module.Decl.CallBroker.Source != "circleci" {
				continue
			}
			program := module.Decl.CallBroker.Program
			path, ok := o.LookPath(program)
			if !ok {
				r.fail("circleci: host CLI not found", "Install the CircleCI CLI on the host, complete its host-side login with `circleci auth login`, then rerun `yolo check`.")
				continue
			}
			r.ok("circleci: host CLI found at " + path)
			r.note("circleci: login status is not checked by `yolo check`. If a call reports an authentication failure, run `circleci auth login` on the host and retry; no credential or account is probed here.")
		}
	}
}
