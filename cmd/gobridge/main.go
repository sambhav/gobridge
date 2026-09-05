// Command gobridge generates bridge adapters from annotated Go source.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/sambhav/gobridge/internal/sourcegen"
)

var version string

func toolVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

func run(args []string, stderr io.Writer) error {
	return runContext(context.Background(), args, stderr)
}

func runContext(ctx context.Context, args []string, stderr io.Writer) error {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(stderr, "gobridge", toolVersion())
		return nil
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stderr, "Usage: gobridge init [--module example.com/project] [--name acme.greeter]")
		fmt.Fprintln(stderr, "       gobridge generate [--dir .] [--output zz_gobridge.gen.go] [--check]")
		fmt.Fprintln(stderr, "       gobridge dev [--once] [--typescript | --python build/<name>] [-- python app.py]")
		fmt.Fprintln(stderr, "       gobridge build [--python] [--typescript] [--check] [--replace] [--output dist]")
		return nil
	}
	if args[0] == "init" {
		return runInit(args[1:], stderr)
	}
	if args[0] == "dev" {
		return runDev(ctx, args[1:], stderr)
	}
	if args[0] == "build" {
		return runBuild(ctx, args[1:], stderr)
	}
	if args[0] != "generate" {
		return fmt.Errorf("unknown command %q; use gobridge --help", args[0])
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runContext(ctx, os.Args[1:], os.Stderr); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "gobridge:", err)
		os.Exit(1)
	}
}
