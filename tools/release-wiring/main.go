package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: release-wiring validate-assets [flags] | claim-publication | check-version-order | verify-release-request | verify-resume | verify-publisher-provenance | validate-wheels [flags]")
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "validate-assets":
		code = writeReleaseAssets(os.Args[2:], os.Stdout, os.Stderr)
	case "claim-publication":
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "claim-publication takes no arguments; all provenance comes from validated workflow context")
			code = 2
		} else if err := claimFromEnv(context.Background(), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "claim-publication: %v\n", err)
			code = 1
		}
	case "check-version-order":
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "check-version-order takes no arguments")
			code = 2
		} else if err := versionOrderFromEnv(context.Background(), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "check-version-order: %v\n", err)
			code = 1
		}
	case "verify-release-request":
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "verify-release-request takes no arguments")
			code = 2
		} else if err := verifyReleaseRequestFromEnv(context.Background(), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "verify-release-request: %v\n", err)
			code = 1
		}
	case "verify-resume":
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "verify-resume takes no arguments")
			code = 2
		} else if err := verifyResumeFromEnv(context.Background(), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "verify-resume: %v\n", err)
			code = 1
		}
	case "verify-publisher-provenance":
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "verify-publisher-provenance takes no arguments")
			code = 2
		} else if err := verifyPublisherProvenanceFromEnv(context.Background(), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "verify-publisher-provenance: %v\n", err)
			code = 1
		}
	case "validate-wheels":
		code = validateWheelsCLI(os.Args[2:], os.Stdout, os.Stderr)
	default:
		fmt.Fprintf(os.Stderr, "release-wiring: unknown command %q\n", os.Args[1])
		code = 2
	}
	os.Exit(code)
}
