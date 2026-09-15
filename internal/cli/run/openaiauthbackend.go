package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const openAIAuthBrokerName = "openai-auth-broker"

func openAIAuthLoopholeActive(cfg *jsonx.OrderedMap) bool {
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	lp, ok := set.Lookup(openAIAuthBrokerName)
	return ok && lp.Active() && set.MayRunHostCode(lp)
}

func (o *Options) startOpenAIAuth(cname, rt string, cfg *jsonx.OrderedMap) []loopholeDaemon {
	return o.startLoopholesMatching(cname, rt, cfg, func(name string) bool {
		return name == openAIAuthBrokerName
	})
}

func withoutOpenAIAuthPack(packs []*packload.Pack) []*packload.Pack {
	out := make([]*packload.Pack, 0, len(packs))
	for _, p := range packs {
		if p.Name != "openai-auth" {
			out = append(out, p)
		}
	}
	return out
}
