package cli

// describe.go is `yolo describe` and `yolo apply` — the two new verbs of the
// environment-manager framing (env-manager plan Phase 3, design §3.1/§3.2).
//
//	describe   print the resolved environment description (human, --json, --hash)
//	apply      make the environment match its description (jail-first)
//
// describe is the reproducibility claim made checkable: the description is a thing you
// can hold. --json is the canonical computed config (the same bytes `config dump`
// prints and the startup diff validates); --hash is a cache key / CI pin over it. The
// --hash caveat (§3.2): a hash over an UNSEALED environment moves for reasons the user
// cannot enumerate, so it is printed MARKED until sealing (Phase 5) makes it authoritative.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func runDescribe(args []string) int {
	return describeMain(args[1:], os.Stdout, os.Stderr, colorForWriter(os.Stdout))
}

func describeMain(args []string, out, errw io.Writer, color bool) int {
	var asJSON, asHash bool
	for _, a := range args {
		switch {
		case isHelpToken(a):
			io.WriteString(out, describeUsage+"\n")
			return 0
		case a == "--json":
			asJSON = true
		case a == "--hash":
			asHash = true
		default:
			fmt.Fprintf(errw, "yolo describe: unexpected argument %q\n\n%s\n", a, describeUsage)
			return 2
		}
	}
	cfg, err := config.LoadConfig("", false, func(string) {})
	if err != nil {
		fmt.Fprintf(errw, "yolo describe: %v\n", err)
		return 1
	}
	canonical, err := config.SnapshotJSON(cfg)
	if err != nil {
		fmt.Fprintf(errw, "yolo describe: %v\n", err)
		return 1
	}

	if asJSON {
		// The canonical computed config — supersedes `config dump` (same bytes).
		fmt.Fprintln(out, canonical)
		return 0
	}
	if asHash {
		sum := sha256.Sum256([]byte(canonical))
		// MARKED, not bare: until `apply --sealed` (Phase 5) proves the environment was
		// assembled only from declared inputs, this hash can move for reasons the user
		// cannot enumerate — so it is not yet an authoritative "same env" pin (§3.2).
		fmt.Fprintf(out, "sha256:%s  (UNSEALED — not authoritative until `apply --sealed`; see `yolo apply --help`)\n",
			hex.EncodeToString(sum[:]))
		return 0
	}

	// Human summary. Deliberately compact — the machine-readable answer is --json.
	pr := richtext.Printer{W: out, Color: color}
	conf := config.ResolveConfinement(cfg)
	pr.Printf("[bold]environment[/bold]  confinement [cyan]%s[/cyan]", string(conf))
	if packs, perr := config.LoadPacks(nil); perr == nil && len(packs) > 0 {
		names := make([]string, 0, len(packs))
		for _, p := range packs {
			names = append(names, p.Name)
		}
		pr.Printf("[bold]packs[/bold]        %s", joinComma(names))
	} else {
		pr.Printf("[bold]packs[/bold]        [dim](none configured)[/dim]")
	}
	sum := sha256.Sum256([]byte(canonical))
	pr.Printf("[bold]description[/bold]  sha256:%s [dim](unsealed — `describe --hash` for the pin, `describe --json` for the full config)[/dim]",
		hex.EncodeToString(sum[:])[:16])
	return 0
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

const describeUsage = `yolo describe — print the resolved environment description

The description is the product: what tools, agents, config, and confinement level this
environment resolves to. It is meant to be a thing you can hold and compare.

  yolo describe          human-readable summary (confinement, packs, description hash)
  yolo describe --json   the full canonical computed config (supersedes 'config dump')
  yolo describe --hash   a sha256 pin over the canonical config, for CI / cache keys

The hash is printed MARKED as unsealed: until 'yolo apply --sealed' (which refuses any
undeclared input), the environment can differ from its description in ways the hash
cannot see, so it is a cache key, not yet a reproducibility guarantee.`
