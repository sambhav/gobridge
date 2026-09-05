# Embed only the daemon in an existing Cobra CLI

`Registry.Serve` supports direct embedding. The standalone Cobra example and its
integration tests demonstrate this contract with a real Cobra dependency.

Mount a `serve` command in your existing command tree. Cobra owns command parsing,
help, flags, hooks, and your other commands; the bridge handles the private stdio
session inside `RunE`.

```go
import (
    "os"

    "github.com/sambhav/gobridge"
    "github.com/spf13/cobra"
)

func addDaemon(root *cobra.Command, registry *gobridge.Registry) {
    root.SetIn(os.Stdin)
    root.SetOut(os.Stdout)
    root.SetErr(os.Stderr)

    root.AddCommand(&cobra.Command{
        Use:           "serve",
        Short:         "Run the private library daemon over stdio",
        Args:          cobra.NoArgs,
        SilenceUsage:  true,
        SilenceErrors: true,
        RunE: func(cmd *cobra.Command, args []string) error {
            return registry.Serve(
                cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), 64,
            )
        },
    })
}
```

Pass a registry built with `Bind`, `Register`, or generated `NewGobridge`.
`Serve` returns an error to Cobra and never calls `os.Exit`. Your executable
handles the final error and exit status. Call `Serve` in an embedded command;
`Registry.Main` is the entrypoint for a standalone bridge executable.

Cobra's `OutOrStdout` and `InOrStdin` use configured streams, including inherited
parent streams, with standard output and input as their fallbacks. Explicit
`SetOut` and `SetErr` make the protocol and diagnostics destinations clear.
`ExecuteContext` supplies the context returned by `cmd.Context()`.
[Cobra command API](https://pkg.go.dev/github.com/spf13/cobra#Command),
[Cobra stream implementation](https://github.com/spf13/cobra/blob/v1.10.2/command.go).

## Nest it anywhere in the host's command tree

To expose `host bridge serve`, mount the same daemon command beneath a Cobra
command named `bridge`. The host's `version`, `status`, and other commands remain
ordinary Cobra commands. Only `serve` needs to be mounted: exposing the bridge's
operation CLI, `schema`, or `generate-python` commands is optional.

```go
bridge := &cobra.Command{
    Use:           "bridge",
    Short:         "Private library integration",
    Args:          cobra.NoArgs,
    SilenceUsage:  true,
    SilenceErrors: true,
    RunE: func(cmd *cobra.Command, args []string) error {
        return cmd.Help()
    },
}
addDaemon(bridge, registry)
root.AddCommand(bridge)
```

Giving the group an argument check and a help action also makes an unmounted
command such as `host bridge greet` return an error instead of silently showing
the group's help.

Point the generated Python client at the command prefix:

```python
from annotated import Greeter
from gobridge import RuntimeOptions

with Greeter(
    prefix="Hey, ",
    _runtime=RuntimeOptions(command=["./host", "bridge"]),
) as greeter:
    assert greeter.welcome(name="Sam") == "Hey, Sam"
```

The runtime appends `serve`, launching `./host bridge serve`. Supply command
arguments as a list; there is no shell command parsing. Constructor options still
travel through the initialization protocol, and the generated schema check still
protects against loading bindings for a different registry. Generate those
bindings from the same registry during your build; the shipped host only needs
the daemon entrypoint.

For importable module functions, configure the default client before its first
call:

```python
from annotated import control, greet
from gobridge import RuntimeOptions

control.configure(_runtime=RuntimeOptions(command=["./host", "bridge"]))
assert greet(name="World") == "Hello, World!"
control.close()
```

## Stream ownership and shutdown

While `serve` runs, stdout contains only bridge protocol frames. Write logs,
warnings, and host startup diagnostics to stderr. Cobra's persistent hooks also
need to respect this rule. The command suppresses automatic error/usage printing;
the host prints a returned error to stderr once. Explicit `--help` remains normal
Cobra help before a daemon session begins.

The host supplies a cancellable context to `root.ExecuteContext(ctx)`. Cancellation
reaches active Go operations. Because `Serve` reads from a caller-supplied
`io.Reader`, the host must also unblock an idle read when shutting down. For an
executable that owns stdin, close it when the execution context is canceled:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
defer stop()
stopRead := context.AfterFunc(ctx, func() { _ = os.Stdin.Close() })
defer stopRead()

if err := root.ExecuteContext(ctx); err != nil {
    fmt.Fprintln(os.Stderr, err)
    // Select the host application's exit status here.
}
```

An embedding application using a borrowed stream keeps ownership of it and
provides its own shutdown/unblock mechanism. `Serve` does not close arbitrary
reader or writer values. EOF ends the session and cancels active requests;
handlers must honor their contexts. Create a fresh registry for each independent
session when it declares a constructor. The example host ends after its selected
command returns, letting its main function own process cleanup.

## Runnable Cobra example

`examples/cobra` is a separate Go module with a pinned Cobra dependency and a
local replacement for the parent `gobridge` module. The core library keeps its
standard-library-only dependency graph. From the repository root:

```sh
cd examples/cobra
go test -race ./...
go build -o host .
./host --help
./host version
./host bridge serve --help
```

The example imports the native `greeter` library and mounts only its daemon at
`host bridge serve`. Its tests exercise actual Cobra parsing, the host's help and
commands, stdio handshakes and calls, stream separation, and cancellation.
