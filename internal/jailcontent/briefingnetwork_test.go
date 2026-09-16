package jailcontent

// The bridge-mode network paragraph, and the one thing it must not do: assert a
// numeric address for `host.containers.internal`.
//
// It asserted 169.254.1.2 until 2026-09-16. That is the address yolo asks pasta to
// map the host's loopback to — one of three answers a jail can get, and wrong on the
// other two. Under slirp4netns podman aims the name at the host's GLOBAL address
// (internal/cli/run/hostloopback.go states this in its own table), and on a macOS
// podman machine gvproxy answers 192.168.127.254 (measured 2026-09-16, podman 6.0.2
// on applehv, with data crossing both ways). So the briefing was telling every macOS
// agent an address that resolves to nothing on its host.
//
// The name is the contract and the number is a property of a host stack this package
// cannot see, which is why the fix is to name the lookup rather than a better default:
// a second hardcoded address would just be wrong on a different host.

import (
	"regexp"
	"strings"
	"testing"
)

// dottedQuad matches an IPv4 literal. Deliberately not anchored to a specific
// address: the point is that no HOST-DEPENDENT address belongs in this line, so a
// future edit that swaps 169.254.1.2 for 192.168.127.254 fails here too.
var dottedQuad = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

// 127.0.0.1 is the one literal the line may carry, and it is not an exception to the
// rule so much as a different subject: it names "the host's own loopback", which is
// the same address on every host, and the sentence exists to say that services bound
// there are reachable. The gateway's address is the host-dependent one.
const hostLoopbackLiteral = "127.0.0.1"

func TestBriefingBridgeLineAssertsNoNumericGatewayAddress(t *testing.T) {
	line := networkLineOf(t, BriefingInput{Workspace: "/w", NetMode: "bridge"})

	if !strings.Contains(line, "host.containers.internal") {
		t.Fatalf("bridge line does not name the gateway at all: %q", line)
	}
	if !strings.Contains(line, hostLoopbackLiteral) {
		t.Errorf("bridge line no longer says host services on %s are reachable, which is the "+
			"fact it exists to carry:\n%s", hostLoopbackLiteral, line)
	}
	if m := dottedQuad.FindString(strings.ReplaceAll(line, hostLoopbackLiteral, "")); m != "" {
		t.Errorf("bridge line asserts the numeric address %q; the address depends on the "+
			"host's network stack (pasta, slirp4netns and a macOS podman machine each give a "+
			"different one), so the line must name the lookup instead:\n%s", m, line)
	}
	// Naming the lookup is what replaces the address: an agent that needs the number
	// has to be told how to get it, or removing the wrong one leaves a gap.
	if !strings.Contains(line, "getent hosts host.containers.internal") {
		t.Errorf("bridge line drops the address without naming the lookup that replaces it:\n%s", line)
	}
}

// `unknown` is the value every macOS podman launch carries, because the launcher
// excludes itself from the host-loopback decision by name there — while the hop
// itself works (gvproxy forwards it with no flag asked for, measured). A line that
// names only requested/shared therefore reads as "unknown means no forwarding",
// which is false exactly where the value is always unknown, and an agent that
// believes it skips a working route.
func TestBriefingBridgeLineDoesNotReadUnknownAsUnreachable(t *testing.T) {
	line := networkLineOf(t, BriefingInput{Workspace: "/w", NetMode: "bridge"})

	if !strings.Contains(line, "YOLO_HOST_LOOPBACK") {
		t.Fatalf("bridge line does not mention the disposition variable: %q", line)
	}
	if !strings.Contains(line, "unknown") {
		t.Errorf("bridge line explains requested/shared but never `unknown`, which is the "+
			"value every macOS podman jail gets on a hop that works:\n%s", line)
	}
}

// Host networking has no gateway hop to describe, so the address question cannot
// arise there — the guard above would pass vacuously if the paragraph were emitted
// in both modes. This pins that it is not.
func TestBriefingHostModeLineNamesNoGateway(t *testing.T) {
	line := networkLineOf(t, BriefingInput{Workspace: "/w", NetMode: "host"})

	if strings.Contains(line, "host.containers.internal") {
		t.Errorf("host-mode line describes a gateway hop that does not exist under a shared "+
			"network stack:\n%s", line)
	}
}
