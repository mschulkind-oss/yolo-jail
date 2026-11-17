package awsclassify

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
)

type ceRow struct{ required, optional []string }

var ceRows = map[string]ceRow{
	"get-cost-and-usage":   {[]string{"time-period", "granularity", "metrics"}, []string{"group-by", "filter", "next-page-token"}},
	"get-cost-forecast":    {[]string{"time-period", "granularity", "metric"}, []string{"filter", "prediction-interval-level"}},
	"get-dimension-values": {[]string{"time-period", "dimension"}, []string{"search-string", "filter", "max-results", "next-page-token", "context"}},
	"get-tags":             {[]string{"time-period"}, []string{"tag-key", "search-string", "filter", "max-results", "next-page-token"}},
}

var ceFlags = map[string]bool{
	"time-period": true, "granularity": true, "metrics": true, "group-by": true, "filter": true,
	"next-page-token": true, "metric": true, "prediction-interval-level": true, "dimension": true,
	"search-string": true, "max-results": true, "context": true, "tag-key": true,
}

const ceNext = "Use `aws ce get-cost-and-usage`, `aws ce get-cost-forecast`, `aws ce get-dimension-values` or `aws ce get-tags` with the supported query flags, or run the command on the host"

func ceUnsupported(answer callbroker.Answer) callbroker.Answer {
	return refused(answer, "unsupported", "unsupported Cost Explorer query form or value", ceNext)
}

func classifyCE(answer callbroker.Answer, input callbroker.CLIInput) callbroker.Answer {
	argv := input.Argv
	if len(argv) < 2 {
		return ceUnsupported(answer)
	}
	row, ok := ceRows[argv[1]]
	if !ok {
		return ceUnsupported(answer)
	}
	allowed := map[string]bool{}
	for _, name := range append(append([]string{}, row.required...), row.optional...) {
		allowed[name] = true
	}
	values := map[string][]string{}
	for i := 2; i < len(argv); {
		word := argv[i]
		i++
		if !strings.HasPrefix(word, "--") {
			return ceUnsupported(answer)
		}
		name, value, equal := strings.Cut(word[2:], "=")
		if !allowed[name] {
			return ceUnsupported(answer)
		}
		if _, exists := values[name]; exists {
			return ceUnsupported(answer)
		}
		operands := []string{}
		if equal {
			operands = append(operands, value)
		}
		list := name == "metrics" || name == "group-by"
		if list {
			for i < len(argv) && !strings.HasPrefix(argv[i], "--") {
				operands = append(operands, argv[i])
				i++
			}
		} else if !equal {
			if i == len(argv) || strings.HasPrefix(argv[i], "--") {
				return ceUnsupported(answer)
			}
			operands = append(operands, argv[i])
			i++
		}
		if len(operands) == 0 {
			return ceUnsupported(answer)
		}
		for _, v := range operands {
			if !ceBoundary(v) {
				return ceUnsupported(answer)
			}
		}
		values[name] = operands
	}
	for _, name := range row.required {
		if _, ok := values[name]; !ok {
			return ceUnsupported(answer)
		}
	}
	if argv[1] == "get-dimension-values" {
		if _, ok := values["context"]; !ok {
			values["context"] = []string{"COST_AND_USAGE"}
		}
	}
	canon := []string{"ce", argv[1]}
	for _, name := range append(append([]string{}, row.required...), row.optional...) {
		raw, present := values[name]
		if !present {
			continue
		}
		parsed, ok := ceValue(name, raw)
		if !ok {
			return ceUnsupported(answer)
		}
		for _, v := range parsed {
			if !ceBoundary(v) {
				return ceUnsupported(answer)
			}
		}
		canon = append(canon, "--"+name)
		canon = append(canon, parsed...)
	}
	covered := false
	for _, root := range input.Roots {
		covered = covered || root == "ce://host-login"
	}
	if !covered {
		return refused(answer, "scope", "no approved Cost Explorer root ce://host-login covers this query", "Add `ce://host-login` to `call_roots.aws` (TOML) or `brokered.aws.roots` (JSONC) in this workspace's config, then run `yolo config apply` on the host")
	}
	canon = append(canon, "--output", "json", "--no-paginate")
	return read(answer, input, canon, "cost_query", "ce://host-login/"+argv[1], "ce://host-login")
}
