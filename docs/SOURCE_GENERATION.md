# Source generation: expose Go code without handwritten bridge adapters

**Design-first checkpoint:** this document precedes implementation of the
annotation generator. `Bind` and object registration remain the lower-level
building blocks; source generation should make those declarations unnecessary
for the common case.

## The intended Go experience

```go
//go:generate gobridge generate --dir . --output zz_gobridge.gen.go

//gobridge:export
func Greet(name string) string {
    return "Hello, " + name + "!"
}

type Options struct {
    Prefix *string `json:"prefix,omitempty"`
}

//gobridge:constructor
func NewGreeter(options Options) (*Greeter, error) {
    // Apply normal Go functional options here, if the underlying library uses them.
    return newGreeter(options), nil
}

//gobridge:export
func (g *Greeter) Welcome(name string) string {
    return g.prefix + name
}
```

Run the generator and get a `NewGobridge() (*gobridge.Registry, error)` function
in the same package. A tiny main calls `NewGobridge`, checks its error, and runs
`registry.Main()`. A normal Go library can call `NewGobridge` from a separate
command package, keeping business logic independent of the CLI entrypoint. Go
consumers import the library package and call its ordinary functions and methods
directly; see [GO_CONSUMER.md](GO_CONSUMER.md) for a complete native Go example.

The generator reads Go declarations with the standard Go parser. Parameter names
come from source and become snake_case (`userID` becomes `user_id`), and methods
use method expressions; constructors are not
executed during discovery or binding generation. No unsafe runtime parameter-name
guessing, Python C extensions, or cgo glue is required.

## Annotation contract for the first version

| Declaration | Annotation | Generated behavior |
| --- | --- | --- |
| Function | `//gobridge:export` | Expose snake_case function name |
| Function/method | `//gobridge:export custom_name` | Expose explicit operation name |
| Constructor | `//gobridge:constructor` | One process-owned object; options map to Python init |
| Method | `//gobridge:export` | Bind receiver method to that object |

Opt in explicitly. Importing a package or adding an exported Go helper must not
silently expose new operations. Unnamed/blank parameters, unsupported generic or
variadic signatures, multiple constructors, name collisions and unmatched
receivers produce actionable generation errors. Compile-time checking and the
registry's wire-type validation still apply to the generated code.

The first version scans a single package directory, ignores test/generated
output, and respects the host's Go build constraints. Run generation for the
package's intended build configuration. Cross-compilation remains a build step
after generation; schemas should be stable across targets where the same API is
supported. Generated adapters and Python bindings are committed and checked for
drift in CI.

`gobridge generate --dir . --check` compares the adapter with its expected source
without writing anything; it exits with an actionable error when output is missing
or stale. Go doc comments become operation descriptions in the generated schema
and CLI help. `//gobridge:` annotations themselves are omitted from descriptions.

## Run the annotated example

From the repository root:

```sh
go generate ./examples/annotated
go run ./examples/annotated/cmd/annotated --config '{"prefix":"Hey, "}' welcome --name World
go run ./cmd/gobridge generate --dir examples/annotated --check
```

The first command generates `examples/annotated/zz_gobridge.gen.go` using the
repository's CLI. The second prints `"Hey, World"`; the third checks that the Go
adapter is up to date. The example includes a plain `greet` function, configured
`welcome` method, typed `stats` result, and a `reset` method returning no value.
Its atomic counter demonstrates that concurrent calls share the owning Go
instance safely.

The annotated declarations live in importable package `greeter` at
`examples/annotated`. Its executable entrypoint lives in
`examples/annotated/cmd/annotated`; build that command package for CLI, daemon,
and Python wheel use. Native Go consumers import the library package directly.

Install the generator from this checkout, then add the directive from the
opening example to your own project:

```sh
go install ./cmd/gobridge
```

Run `go generate ./...` inside your project with the installed `gobridge` on
`PATH`. Pin the module version when adopting the library. The generator and runtime
should come from the same version. The generated adapter is ordinary Go source;
your existing build produces the daemon and its `generate-python` command.

## Python should see the library, not the adapter

```python
from greeter import greet

message = greet(name="World")

from greeter import Greeter
with Greeter(prefix="Welcome, ") as greeter:
    message = greeter.welcome(name="Sam")
```

Plain Go return values become plain Python values. Structured Go return values
become typed dataclasses. An optional Go context does not become a Python argument;
timeouts and cancellation supply it. Go errors become Python exceptions.

The platform wheel contains the generated bindings and cross-compiled Go
executable as package data. Module functions reuse the lazy process-local default
client. Configured instances and context scopes retain explicit ownership, as
described in [API_DESIGN.md](API_DESIGN.md).

## Struct tags and metadata: one contract

`json:"name"` defines the wire/Python field name; pointer `omitempty` fields map
to optional Python values. Arbitrary Go closures/functional options cannot be
serialized: expose their data through an options struct and apply them once in
the constructor. Source annotations remove registration boilerplate, not that
semantic boundary.

Follow-up metadata should use explicit tags/comments for descriptions, defaults
and constraints. Go runtime validation, Python signatures/help and generated
documentation must agree; avoid introducing Python-only defaults or validators
that diverge from CLI behavior. Richer tags land only with that shared behavior
and tests. Annotation parsing itself must never execute project code.

## Acceptance checks

- Generate from real package source; compile the adapter and run its CLI.
- Generate Python from the compiled schema; test module functions and configured
  object methods end to end.
- Verify actual parameter names, explicit renames, optional contexts, receiver
  selection and useful rejection errors.
- Regeneration is deterministic and never overwrites a handwritten output file.
- Test annotated examples across the existing OS/Python CI matrix.
