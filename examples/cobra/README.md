# Cobra host example

This separate Go module mounts only `host bridge serve`; the host owns its
`version` and `status` commands. From this directory, run `go test -race ./...`
and `go build -o host .`. The local module replacement points to the repository root.

See the [user guide](../../README.md#9-embed-only-the-daemon-in-cobra) for embedding.
