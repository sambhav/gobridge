// Command host embeds the greeter daemon in an otherwise ordinary Cobra CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	greeter "github.com/sambhav/gobridge/examples/annotated"
	"github.com/spf13/cobra"
)

func newRoot() (*cobra.Command, error) {
	registry, err := greeter.NewGobridge()
	if err != nil {
		return nil, err
	}
	root := &cobra.Command{
		Use:           "host",
		Short:         "An existing CLI with an embedded library daemon",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the host application's version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "host development")
			return err
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Print the host application's status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "ready")
			return err
		},
	})
	bridge := &cobra.Command{
		Use:   "bridge",
		Short: "Private library integration",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	bridge.AddCommand(&cobra.Command{
		Use:           "serve",
		Short:         "Run the private library daemon over stdio",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return registry.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), 64)
		},
	})
	root.AddCommand(bridge)
	return root, nil
}

// execute owns in for this execution. Closing that input unblocks Serve's read
// on context cancellation. A host using a borrowed stream should retain stream
// ownership and supply its own unblock mechanism instead.
func execute(ctx context.Context, args []string, in io.ReadCloser, out, errOut io.Writer) error {
	defer in.Close()
	stopRead := context.AfterFunc(ctx, func() { _ = in.Close() })
	defer stopRead()
	root, err := newRoot()
	if err != nil {
		return err
	}
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errOut)
	err = root.ExecuteContext(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
