# A discoverable CLI for the same Go operations

**Implemented increment.** This document was committed before the CLI help
change. Operation help, top-level aliases, constructor discovery and strict
metadata-argument handling are now covered by Go tests alongside the existing
operation calls, JSON input/output, schema inspection and Python generation.

## Call operations directly or pipe JSON

```sh
hello greet --name world
hello greet --json '{"name":"world"}'
hello greet --json - < input.json
```

Successful operation calls write one JSON result to stdout and no progress
text. Strings can be passed as ordinary flag values. Numbers, booleans, arrays,
maps and nested objects use JSON literals. JSON field names containing
underscores have kebab-case flags: `process_id` becomes `--process-id`.

Optional pointer fields may be omitted. For explicit null, including a nullable
string, use the JSON form, for example `--json '{"name":null}'`. Non-pointer
fields remain required. A required slice or map can accept JSON null; accepting
null and allowing omission are distinct properties.

The CLI and Python clients call the same registered handlers and share the
same Go validation. A direct CLI invocation exits after one operation, so it
does not preserve an in-memory cache between separate shell commands.

## Find an operation and its flags

```sh
hello
hello --help
hello -h
hello help
hello greet --help
hello greet -h
hello help greet
```

Top-level help lists available operations, descriptions, and the built-in
`serve`, `schema`, and `generate-python` commands. It points to per-operation
help instead of requiring users to inspect a JSON schema for ordinary flags.

All three operation-help forms show the same information:

- Operation name and description.
- Invocation using field flags, a JSON object, or JSON from stdin.
- Each field's kebab-case flag, wire type, and required/optional status.
- Field documentation and declared numeric or length limits, from the same
  schema used by the Go validator and Python generator.
- The returned scalar or named Go model type.
- Constructor configuration fields, when the registry declares a constructor.

For example, `hello greet --help` identifies `--name` as a required string and
the result as `Greeting`. A bound Go function returning `string` reports a
string result; a function without a result reports null. Structs use their Go
type names, and scalar types use familiar names such as string, boolean,
integer, and number. Pointer/container descriptions show whether null is
accepted. Schema inspection remains available for complete nested fields,
including fields inside arrays and map values:

```sh
hello schema
```

Help is a metadata operation. It does not call handlers, initialize a Go
object, or validate credentials. No constructor is needed to inspect a command.

### Document fields and discover nested JSON objects

Go `doc` and `validate` tags are visible in help:

```go
type GreetingRequest struct {
    Name string `json:"name" doc:"Name to greet." validate:"minlen=1,maxlen=80"`
    Age  *int   `json:"age,omitempty" doc:"Optional age in years." validate:"min=0,max=120"`
}
```

The name's help includes `Name to greet.` and `minlen=1, maxlen=80`. Numeric
limits use `min` and `max`; length limits use `minlen` and `maxlen`, matching the
declarations. Numeric limits retain their exact values, including large int64
bounds. Constructor fields use the same rendering.

When a bound Go function accepts a request struct, its CLI accepts one JSON
object flag:

```sh
greeter greet --request '{"name":"Ada","age":37}'
```

Help lists `--request` as the actual flag. A separate nested JSON section shows
`request.name` and `request.age`, including their descriptions and constraints.
These paths identify JSON members; they are not additional flags. Deeper named
structs get paths such as `request.address.city`. Requiredness applies when the
containing object is supplied; an optional parent can still be omitted or null.
Arrays and maps show their element/value type names; use `schema` for their
complete nested definitions. See [field metadata](FIELD_METADATA.md) for the
shared validation contract.

## Discover and supply constructor configuration

For an object-backed library, help shows `--config OBJECT` before the operation
and lists constructor fields using their JSON names, types and required/optional
status. These fields are JSON keys, not additional operation flags:

```sh
counter increment --help
counter --config '{"initial":10}' increment --amount 2
counter --config '{"initial":10}' increment --help
```

The final command still only displays help; it does not execute the constructor.
Configuration is validated when an operation is called. If `--config` is
omitted, the constructor receives `{}`; required configuration fields must still
be provided. Supplying `--config` to a registry with no constructor is an error.
`serve`, `schema`, `generate-python`, and top-level help do not accept it.

## Strict metadata commands

```sh
hello schema
hello generate-python --class Hello --binary hello > hello.py
hello serve --max-concurrency 64
```

Metadata commands reject unexpected trailing arguments. For example,
`hello schema extra`, `hello help greet extra`, and
`hello generate-python --class Hello stray` fail instead of silently accepting
typos. An unknown help target also fails. Built-in flag parsing retains its
standard diagnostics; operations and metadata must not run after a parse error.

Calling `Registry.Run` returns an error to the embedding program. The normal
`Registry.Main` entrypoint reports the structured error on stderr and exits
nonzero. Operation stdout remains suitable for JSON pipelines.

See [Hello World](HELLO_WORLD.md) for a complete runnable example and
[Go API](GO_API.md) for constructors and direct function binding.
