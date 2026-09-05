# Cobra host example

This separate Go module mounts the annotated Greeter library's daemon at
`host bridge serve`. The `version` and `status` commands belong to the host.

From this directory:

```sh
go test -race ./...
go build -o host .
./host --help
./host version
```

Use Python's `RuntimeOptions(command=["./host", "bridge"])` with the generated
`annotated.Greeter` bindings. The runtime adds `serve` to the command prefix.

See [the embedding guide](../../docs/EMBEDDING.md) for stream ownership,
cancellation, and integration into an existing command tree. The module pins
Cobra separately so the core `gobridge` package keeps its standard-library-only
dependencies. Its local module replacement points to the repository root.
