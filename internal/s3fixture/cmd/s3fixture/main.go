// Command s3fixture is a test and demo fixture. It is never shipped and never
// contacts AWS. Under the name `aws` it is a fake host aws CLI that speaks only the
// S3 mediation slice's canonical grammar; run as `s3fixture serve` it is a local,
// path-style S3 subset on 127.0.0.1 that accepts only fixed SYNTHETIC credentials.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/s3fixture"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

func main() {
	if filepath.Base(os.Args[0]) == "aws" {
		os.Exit(s3fixture.RunAWS(os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: s3fixture serve [--addr 127.0.0.1:0] [--seed demo|none]\n(or run it under the name `aws` for the fake host CLI)")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:0", "loopback address to listen on")
	seed := fs.String("seed", "demo", "seed data: demo or none")
	_ = fs.Parse(os.Args[2:])

	buckets := map[string]map[string][]byte{}
	switch *seed {
	case "demo":
		buckets["demo-bucket"] = map[string][]byte{
			"hello.txt":         []byte("hello from the synthetic fixture\n"),
			"team/notes.txt":    []byte("team notes\n"),
			"team/sub/data.csv": []byte("a,b\n1,2\n"),
		}
		buckets["other-bucket"] = map[string][]byte{"private.txt": []byte("a different bucket\n")}
	case "none":
	default:
		fmt.Fprintf(os.Stderr, "unknown --seed %q (demo or none)\n", *seed)
		os.Exit(2)
	}

	srv := s3fixture.NewServer(s3fixture.Options{
		Credentials: sigv4.Credentials{AccessKeyID: s3fixture.AccessKeyID, SecretAccessKey: s3fixture.SecretAccessKey},
		Region:      s3fixture.Region,
		Buckets:     buckets,
		OnRequest: func(r s3fixture.LoggedRequest) {
			fmt.Fprintf(os.Stderr, "%s %s?%s authorized=%t -> %d\n", r.Method, r.Path, r.Query, r.Authorized, r.Status)
		},
	})
	if err := srv.StartAddr(*addr); err != nil {
		fmt.Fprintf(os.Stderr, "s3fixture: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()

	fmt.Printf("s3fixture serving a SYNTHETIC S3 at %s (never AWS)\n", srv.URL())
	fmt.Printf("SYNTHETIC credentials (fixture only, not real):\n  access key id: %s\n  secret:        %s\n  region:        %s\n", s3fixture.AccessKeyID, s3fixture.SecretAccessKey, s3fixture.Region)
	fmt.Printf("Environment for a client:\n  AWS_ENDPOINT_URL_S3=%s\n  AWS_ACCESS_KEY_ID=%s\n  AWS_SECRET_ACCESS_KEY=%s\n  AWS_REGION=%s\n", srv.URL(), s3fixture.AccessKeyID, s3fixture.SecretAccessKey, s3fixture.Region)
	for b := range buckets {
		fmt.Printf("seeded bucket: s3://%s\n", b)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
}
