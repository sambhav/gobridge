// Command gobridge generates bridge adapters from annotated Go source.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sambhav/gobridge/internal/sourcegen"
)

func run(args []string, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stderr, "Usage: gobridge generate [--dir .] [--output zz_gobridge.gen.go] [--check]")
		return nil
	}
	if args[0] != "generate" {
		return fmt.Errorf("unknown command %q; use gobridge generate", args[0])
	}
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", ".", "Go package directory to scan")
	output := flags.String("output", "zz_gobridge.gen.go", "generated Go filename in that directory")
	check := flags.Bool("check", false, "report missing or stale output without changing files")
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *check {
		return sourcegen.Check(*dir, *output)
	}
	return sourcegen.Generate(*dir, *output)
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "gobridge:", err)
		os.Exit(1)
	}
}
