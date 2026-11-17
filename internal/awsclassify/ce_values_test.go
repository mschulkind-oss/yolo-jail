package awsclassify

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const period = "Start=2026-01-01,End=2026-02-01"

func ceQuery(t *testing.T, op string, args []string, wantClass string) []string {
	t.Helper()
	argv := append([]string{"ce", op}, args...)
	a := Classify(input([]string{"ce://host-login"}, argv...))
	if wantClass != "" {
		if a.Classification != nil || a.Refused == nil || a.Refused.Class != wantClass || a.Refused.NextStep == "" {
			t.Fatalf("argv=%q answer=%+v", argv, a)
		}
		return nil
	}
	if a.Classification == nil || a.Refused != nil {
		t.Fatalf("argv=%q answer=%+v", argv, a)
	}
	return a.Classification.Argv
}
func ceTagsArgs(flag, value string) []string {
	return []string{"--time-period", period, "--" + flag, value}
}
func ceUsageArgs(flag string, values ...string) []string {
	args := []string{"--time-period", period, "--granularity", "DAILY"}
	if flag != "metrics" {
		args = append(args, "--metrics", "UnblendedCost")
	}
	args = append(args, "--"+flag)
	return append(args, values...)
}

func TestCEValueGrammarsThroughClassify(t *testing.T) {
	cases := []struct {
		name, op string
		args     []string
		ok       bool
	}{
		{"period JSON whitespace", "get-tags", []string{"--time-period", ` {"Start":"2026-01-01","End":"2026-02-01"} `}, true},
		{"leap year", "get-tags", []string{"--time-period", "End=2024-03-01,Start=2024-02-29"}, true},
		{"first year", "get-tags", []string{"--time-period", "Start=2000-01-01,End=2000-01-02"}, true},
		{"last year", "get-tags", []string{"--time-period", "Start=2099-12-30,End=2099-12-31"}, true},
		{"old year", "get-tags", []string{"--time-period", "Start=1999-01-01,End=2000-01-01"}, false},
		{"future year", "get-tags", []string{"--time-period", "Start=2099-01-01,End=2100-01-01"}, false},
		{"invalid leap", "get-tags", []string{"--time-period", "Start=2026-02-29,End=2026-03-01"}, false},
		{"equal dates", "get-tags", []string{"--time-period", "Start=2026-01-01,End=2026-01-01"}, false},
		{"reverse dates", "get-tags", []string{"--time-period", "Start=2026-02-01,End=2026-01-01"}, false},
		{"short date", "get-tags", []string{"--time-period", "Start=2026-1-01,End=2026-02-01"}, false},
		{"duplicate shorthand", "get-tags", []string{"--time-period", "Start=2026-01-01,Start=2026-02-01"}, false},
		{"period duplicate JSON", "get-tags", []string{"--time-period", `{"Start":"2026-01-01","Start":"2026-01-02","End":"2026-02-01"}`}, false},
		{"period unknown JSON", "get-tags", []string{"--time-period", `{"Start":"2026-01-01","End":"2026-02-01","X":"x"}`}, false},
		{"period type", "get-tags", []string{"--time-period", `{"Start":2026,"End":"2026-02-01"}`}, false},
		{"period trailing", "get-tags", []string{"--time-period", `{"Start":"2026-01-01","End":"2026-02-01"} {}`}, false},
		{"period case", "get-tags", []string{"--time-period", `{"start":"2026-01-01","End":"2026-02-01"}`}, false},
		{"metrics all", "get-cost-and-usage", ceUsageArgs("metrics", ceMetrics...), true},
		{"metrics duplicate", "get-cost-and-usage", ceUsageArgs("metrics", "UnblendedCost", "UnblendedCost"), false},
		{"metrics unknown", "get-cost-and-usage", ceUsageArgs("metrics", "UnblendedCosts"), false},
		{"metrics empty", "get-cost-and-usage", ceUsageArgs("metrics"), false},
		{"metrics JSON", "get-cost-and-usage", ceUsageArgs("metrics", `["UnblendedCost"]`), false},
		{"hourly", "get-cost-and-usage", []string{"--time-period", period, "--granularity", "HOURLY", "--metrics", "UnblendedCost"}, false},
		{"dimension first", "get-dimension-values", []string{"--time-period", period, "--dimension", "A"}, true},
		{"dimension limit", "get-dimension-values", []string{"--time-period", period, "--dimension", strings.Repeat("A", 64)}, true},
		{"dimension too long", "get-dimension-values", []string{"--time-period", period, "--dimension", strings.Repeat("A", 65)}, false},
		{"dimension lowercase", "get-dimension-values", []string{"--time-period", period, "--dimension", "Service"}, false},
		{"dimension starts digit", "get-dimension-values", []string{"--time-period", period, "--dimension", "1SERVICE"}, false},
		{"context explicit", "get-dimension-values", []string{"--time-period", period, "--dimension", "SERVICE", "--context=COST_AND_USAGE"}, true},
		{"context other", "get-dimension-values", []string{"--time-period", period, "--dimension", "SERVICE", "--context", "RESERVATIONS"}, false},
		{"label alphabet", "get-tags", ceTagsArgs("tag-key", "Az09 _.:/=+@-"), true},
		{"label limit", "get-tags", ceTagsArgs("search-string", strings.Repeat("x", 256)), true},
		{"label too long", "get-tags", ceTagsArgs("tag-key", strings.Repeat("x", 257)), false},
		{"label empty", "get-tags", ceTagsArgs("search-string", ""), false},
		{"label unicode", "get-tags", ceTagsArgs("tag-key", "é"), false},
		{"label punctuation", "get-tags", ceTagsArgs("tag-key", "a,b"), false},
		{"max min", "get-tags", ceTagsArgs("max-results", "1"), true},
		{"max max", "get-tags", ceTagsArgs("max-results", "1000"), true},
		{"max zero", "get-tags", ceTagsArgs("max-results", "0"), false},
		{"max overflow", "get-tags", ceTagsArgs("max-results", "1001"), false},
		{"max leading zero", "get-tags", ceTagsArgs("max-results", "01"), false},
		{"max signed", "get-tags", ceTagsArgs("max-results", "+1"), false},
		{"max huge", "get-tags", ceTagsArgs("max-results", strings.Repeat("9", 80)), false},
		{"token min", "get-tags", ceTagsArgs("next-page-token", "a"), true},
		{"token max", "get-tags", ceTagsArgs("next-page-token", strings.Repeat("a", 8192)), true},
		{"token too long", "get-tags", ceTagsArgs("next-page-token", strings.Repeat("a", 8193)), false},
		{"token space", "get-tags", ceTagsArgs("next-page-token", "a b"), false},
		{"token control", "get-tags", ceTagsArgs("next-page-token", "a\n"), false},
		{"token empty", "get-tags", ceTagsArgs("next-page-token", ""), false},
	}
	for _, n := range []string{"50", "51", "99", "100", "051"} {
		cases = append(cases, struct {
			name, op string
			args     []string
			ok       bool
		}{"interval " + n, "get-cost-forecast", []string{"--time-period", period, "--granularity", "MONTHLY", "--metric", "UNBLENDED_COST", "--prediction-interval-level", n}, n == "51" || n == "99"})
	}
	for _, metric := range append(append([]string{}, ceForecastMetrics...), "USAGE_QUANTITY", "UnblendedCost") {
		cases = append(cases, struct {
			name, op string
			args     []string
			ok       bool
		}{"forecast " + metric, "get-cost-forecast", []string{"--time-period", period, "--granularity", "DAILY", "--metric", metric}, metric != "USAGE_QUANTITY" && metric != "UnblendedCost"})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			class := "unsupported"
			if c.ok {
				class = ""
			}
			ceQuery(t, c.op, c.args, class)
		})
	}
}

func TestCEGroupsCanonicalJSONAndBoundaries(t *testing.T) {
	valid := []struct {
		raw  []string
		want string
	}{
		{[]string{"Type=DIMENSION,Key=SERVICE"}, `[{"Type":"DIMENSION","Key":"SERVICE"}]`},
		{[]string{"Type=TAG,Key=file://x", "Type=COST_CATEGORY,Key=-x"}, `[{"Type":"TAG","Key":"file://x"},{"Type":"COST_CATEGORY","Key":"-x"}]`},
		{[]string{` [{"Key":"a=b","Type":"TAG"}] `}, `[{"Type":"TAG","Key":"a=b"}]`},
	}
	for _, c := range valid {
		argv := ceQuery(t, "get-cost-and-usage", ceUsageArgs("group-by", c.raw...), "")
		if argv[len(argv)-4] != c.want {
			t.Fatalf("argv=%q want JSON %s", argv, c.want)
		}
	}
	invalid := [][]string{
		{"Key=SERVICE,Type=DIMENSION"}, {"Type=PRODUCT_ATTRIBUTE,Key=SERVICE"}, {"Type=DIMENSION,Key=service"},
		{"Type=TAG,Key=a=b"}, {"Type=TAG,Key=a,b"}, {"Type=TAG,Key="},
		{"Type=TAG,Key=x", "Type=TAG,Key=x"}, {"Type=TAG,Key=a", "Type=TAG,Key=b", "Type=TAG,Key=c"},
		{`[]`}, {`{}`}, {`null`}, {`[{"Type":"TAG","Key":"x","Other":"y"}]`},
		{`[{"Type":"TAG","Key":"x","Key":"y"}]`}, {`[{"Type":"TAG","Key":2}]`},
		{`[{"type":"TAG","Key":"x"}]`}, {`[{"Type":"TAG","Key":"x"}] {}`},
		{`[{"Type":"TAG","Key":"x"}]`, "Type=TAG,Key=y"}, {"Type=TAG,Key=x", `[{"Type":"TAG","Key":"y"}]`},
		{`[{"Type":"TAG","Key":"x"}]`, `[{"Type":"TAG","Key":"y"}]`},
		{`[{"Type":"TAG","Key":"x"},{"Type":"TAG","Key":"x"}]`},
	}
	for i, raw := range invalid {
		t.Run(fmt.Sprint(i), func(t *testing.T) { ceQuery(t, "get-cost-and-usage", ceUsageArgs("group-by", raw...), "unsupported") })
	}
}

func TestCEVocabularyShapeAndRefusalPrecedence(t *testing.T) {
	globals := []string{"profile", "endpoint-url", "region", "debug", "no-sign-request", "output", "query", "no-paginate", "paginate", "cli-input-json", "cli-input-yaml", "billing-view-arn", "sort-by", "dimension-key", "no-verify-ssl", "ca-bundle", "color", "version"}
	for _, flag := range globals {
		t.Run(flag, func(t *testing.T) { ceQuery(t, "GetAnything", []string{"--" + flag + "=file://secret"}, "credential") })
	}
	for _, op := range []string{"GetCostAndUsage", "get-cost-and-usage-with-resources", "get-usage-forecast", "get-cost-categories", "get-savings-plans-utilization", "create-cost-category-definition", "delete-cost-category-definition", "GetAnything", "describe-anything"} {
		ceQuery(t, op, []string{"--time-period", period}, "unsupported")
	}
	for _, args := range [][]string{
		{}, {"--time-period"}, {"--time-period", period, "extra"},
		{"--time-period", period, "--time-period", period}, {"--time-period", period, "--metrics", "UnblendedCost"},
		{"--time-period", period, "--context", "COST_AND_USAGE"}, {"--time-period", period, "--max-results", "1", "2"},
	} {
		ceQuery(t, "get-tags", args, "unsupported")
	}
	for _, flag := range []string{"filter", "search-string", "tag-key", "next-page-token"} {
		for _, v := range []string{"file://secret", "fileb://secret", "https://secret", "-x", "A1+.-://secret"} {
			ceQuery(t, "get-tags", ceTagsArgs(flag, v), "unsupported")
			ceQuery(t, "get-tags", []string{"--time-period", period, "--" + flag + "=" + v}, "unsupported")
		}
	}
	for _, flag := range []string{"metrics", "group-by"} {
		for _, v := range []string{"file://secret", "-x"} {
			ceQuery(t, "get-cost-and-usage", ceUsageArgs(flag, v), "unsupported")
		}
	}
	for _, service := range []string{"ec2", "sts", "sso", "future"} {
		a := Classify(input([]string{"s3://bkt", "ce://host-login", service + "://all"}, service, "get-anything"))
		if a.Refused == nil || a.Refused.Class != "unsupported" {
			t.Fatalf("future service admitted: %+v", a)
		}
	}
	a := Classify(input(nil, "ce", "get-tags", "--time-period", "bad"))
	if a.Refused == nil || a.Refused.Class != "unsupported" {
		t.Fatalf("value must precede scope: %+v", a)
	}
	a = Classify(input(nil, "ce", "get-tags", "--time-period", "SENSITIVE\nBAD", "--unknown\nSENSITIVE=x"))
	if a.Refused == nil || a.Refused.Class != "credential" || strings.Contains(a.Refused.Reason, "SENSITIVE") {
		t.Fatalf("unsafe refusal %+v", a)
	}
	a = Classify(input([]string{"ce://host-login"}, "s3", "ls", "s3://bkt/"))
	if a.Refused == nil || a.Refused.Class != "scope" {
		t.Fatal("CE root admitted S3")
	}
}

func TestCEFiltersThroughClassify(t *testing.T) {
	leaf := `{"Tags":{"Key":"file://x","Values":["","-x","file://x","https://x","é"],"MatchOptions":["CASE_INSENSITIVE","EQUALS"]}}`
	argv := ceQuery(t, "get-tags", ceTagsArgs("filter", leaf), "")
	want := `{"Tags":{"Key":"file://x","Values":["","-x","file://x","https://x","é"],"MatchOptions":["EQUALS","CASE_INSENSITIVE"]}}`
	if argv[len(argv)-4] != want {
		t.Fatalf("canonical JSON=%q want=%q", argv[len(argv)-4], want)
	}
	invalid := []string{
		`null`, `[]`, `{}`, `{"Unknown":{}}`, `{"Not":[]}`, `{"Or":[]}`, `{"And":[{"Tags":{"Key":"x","Values":["x"]}}]}`,
		`{"Tags":{"Key":"x","Values":[]}}`, `{"Tags":{"Key":"x"}}`, `{"Tags":{"Values":["x"]}}`,
		`{"Tags":{"Key":"x","Values":["x","x"]}}`, `{"Tags":{"Key":"x","Values":[1]}}`, `{"Tags":{"Key":"x","Values":null}}`,
		`{"Tags":{"Key":"x","Values":["a\n"]}}`, `{"Tags":{"Key":"x","Values":["\u007f"]}}`,
		`{"Tags":{"Key":1,"Values":["x"]}}`, `{"Dimensions":{"Key":"service","Values":["x"]}}`,
		`{"Tags":{"Key":"x","Values":["x"],"Unknown":true}}`,
		`{"Tags":{"Key":"x","Values":["x"],"MatchOptions":[]}}`,
		`{"Tags":{"Key":"x","Values":[],"MatchOptions":["ABSENT"]}}`,
		`{"Tags":{"Key":"x","Values":["x"],"MatchOptions":["EQUALS","EQUALS"]}}`,
		`{"Tags":{"Key":"x","Values":["x"],"MatchOptions":["UNKNOWN"]}}`,
		`{"Tags":{"Key":"x","Values":["x"],"MatchOptions":"EQUALS"}}`,
		`{"Tags":{"Key":"x","Values":["x"],"MatchOptions":[1]}}`,
		`{"Tags":{"Key":"x","Key":"y","Values":["x"]}}`,
		`{"Tags":{"Key":"x","Values":["x"],"Values":["y"]}}`,
		`{"Tags":{"Key":"x","Values":["x"]},"Tags":{"Key":"y","Values":["y"]}}`,
		`{"Not":{"Tags":{"Key":"x","Values":["x"]},"Tags":{"Key":"y","Values":["y"]}}}`,
		`{"Tags":{"key":"x","Values":["x"]}}`, `{"Tags":{"Key":"x","Values":["x"]}} true`,
		`{"Tags":{"Key":"x","Values":["` + strings.Repeat("x", 257) + `"]}}`,
		"{\"Tags\":{\"Key\":\"x\",\"Values\":[\"\xff\"]}}",
	}
	for i, s := range invalid {
		t.Run(fmt.Sprint(i), func(t *testing.T) { ceQuery(t, "get-tags", ceTagsArgs("filter", s), "unsupported") })
	}
	for _, kind := range []string{"Dimensions", "Tags", "CostCategories"} {
		s := `{"` + kind + `":{"Values":["x"],"Key":"SERVICE"}}`
		got := ceQuery(t, "get-tags", ceTagsArgs("filter", s), "")
		if strings.Contains(got[len(got)-4], "MatchOptions") {
			t.Fatal("absent MatchOptions was injected")
		}
	}
	base := `{"Tags":{"Key":"x","Values":["x"]}}`
	ceQuery(t, "get-tags", ceTagsArgs("filter", `{"And":[`+base+`,`+base+`]}`), "")
	for _, n := range []int{5, 6} {
		s := base
		for i := 1; i < n; i++ {
			s = `{"Not":` + s + `}`
		}
		class := ""
		if n == 6 {
			class = "unsupported"
		}
		ceQuery(t, "get-tags", ceTagsArgs("filter", s), class)
	}
	for _, n := range []int{50, 51} {
		s := `{"Or":[` + strings.TrimSuffix(strings.Repeat(base+",", n), ",") + `]}`
		class := ""
		if n == 51 {
			class = "unsupported"
		}
		ceQuery(t, "get-tags", ceTagsArgs("filter", s), class)
	}
	for _, n := range []int{8192, 8193} {
		s := base + strings.Repeat(" ", n-len(base))
		class := ""
		if n == 8193 {
			class = "unsupported"
		}
		ceQuery(t, "get-tags", ceTagsArgs("filter", s), class)
	}
	for _, n := range []int{100, 101} {
		vals := []string{}
		for i := 0; i < n; i++ {
			vals = append(vals, fmt.Sprint(i))
		}
		b, _ := json.Marshal(vals)
		s := `{"Tags":{"Key":"x","Values":` + string(b) + `}}`
		class := ""
		if n == 101 {
			class = "unsupported"
		}
		ceQuery(t, "get-tags", ceTagsArgs("filter", s), class)
	}
	vals := []string{strings.Repeat("é", 128)}
	b, _ := json.Marshal(vals)
	ceQuery(t, "get-tags", ceTagsArgs("filter", `{"Tags":{"Key":"x","Values":`+string(b)+`}}`), "")
	options := append([]string{}, ceMatchOptions...)
	for i, j := 0, len(options)-1; i < j; i, j = i+1, j-1 {
		options[i], options[j] = options[j], options[i]
	}
	b, _ = json.Marshal(options)
	got := ceQuery(t, "get-tags", ceTagsArgs("filter", `{"Tags":{"Key":"x","Values":["x"],"MatchOptions":`+string(b)+`}}`), "")
	var node struct{ Tags ceLeaf }
	if err := json.Unmarshal([]byte(got[len(got)-4]), &node); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(node.Tags.MatchOptions, ceMatchOptions) {
		t.Fatal(node)
	}
}
