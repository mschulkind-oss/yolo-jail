package cli

// brokerhintverbs_test.go pins every command the broker grant advice names to the CLI that
// has to run it (AGENTS.md: "Advice names a verb, not a file to hand-edit"): each command the
// git-broker's push hints and the brokers' read-grant refusals print is dispatched by the
// registry and accepted, as a dry run, by a host whose user config selects the shipped git and
// github packs. The hint packages build their text from the same functions this test reads, and
// their own tests pin that the text holds them (gitbroker's convertThenAllow and
// suggestedGrant, config's TestBrokerGrantAdviceNamesYoloAllowToATOMLUser).

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
)

func TestBrokerGrantHintsNameCommandsTheCLIRuns(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\", \"new_branches\", \"push_tags\"]\n\n[allow.github]\nalways = [\"read\"]\n")
	userBefore, fragmentBefore, projectBefore := readFile(t, fx.userConfig), readFile(t, fx.fragment), readFile(t, fx.project)

	commands := []string{
		"yolo config migrate",
		config.BrokerReadAllowCommand(gitbroker.Source, gitbroker.ReadCapability),
		config.BrokerReadAllowCommand(ghbroker.Source, ghbroker.ReadCapability),
	}
	for _, kind := range []string{"create-branch", brokerscope.KindBranch, brokerscope.KindTag} {
		commands = append(commands, gitbroker.AllowOnRequestCommand(kind))
	}
	for _, g := range []brokerscope.Grant{
		{Kind: "create-branch", Repo: "h/o/r"},
		{Kind: brokerscope.KindBranch, Repo: "h/o/r", Branch: "main"},
		{Kind: brokerscope.KindTag, Repo: "h/o/r", Tag: "v1"},
	} {
		commands = append(commands, gitbroker.RequestCommand(g))
	}

	for _, command := range commands {
		argv := strings.Fields(command)
		if len(argv) < 2 || argv[0] != "yolo" {
			t.Fatalf("hint command %q is not a yolo command", command)
		}
		run, ok := registry[argv[1]]
		if !ok {
			t.Fatalf("hint command %q names a verb the CLI does not dispatch", command)
		}
		args := append(append([]string{}, argv[1:]...), "--dry-run")
		rc, out, errs := captureStd(t, func() int { return run(args) })
		if rc != 0 {
			t.Errorf("hint command %q does not run as a dry run: rc=%d stdout=%q stderr=%q", command, rc, out, errs)
		}
	}
	if readFile(t, fx.userConfig) != userBefore || readFile(t, fx.fragment) != fragmentBefore || readFile(t, fx.project) != projectBefore {
		t.Fatal("a dry run of a hint command wrote a config file")
	}

	// The read-grant refusal a TOML user sees holds the command checked above.
	for _, src := range []struct{ source, capability string }{
		{gitbroker.Source, gitbroker.ReadCapability}, {ghbroker.Source, ghbroker.ReadCapability},
	} {
		want := "`" + config.BrokerReadAllowCommand(src.source, src.capability) + "`"
		if got := config.BrokerNoReadRefusal(src.source, src.capability, "h/o/r", true); !strings.Contains(got, want) {
			t.Errorf("the %s read refusal does not name %s:\n%s", src.source, want, got)
		}
	}
}
