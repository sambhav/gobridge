# gobridge

Expose a Go library as a CLI and typed Python and TypeScript packages.
Add comments to ordinary Go functions; gobridge generates the adapters and builds
packages with the Go executable included. Calls reuse a private subprocess, so
Go objects and caches stay alive between calls.

```go
//gobridge:export
func Greet(name string) string { return "Hello, " + name + "!" }
```

```python
from greeter import greet

message = await greet(name="World")  # "Hello, World!"
```

- **Authors:** Go 1.23+ and Python 3.10+; Node 24+ and npm for TypeScript builds.
- **Consumers:** Python 3.10+ or Node 24+. No Go installation or separate server.
- **Scope:** local, typed function calls and stateful objects over subprocess pipes.
  No browser support, streaming, or shared remote daemon.

[Quick start](#quick-start) · [Build packages](#build-and-install-packages) ·
[State and options](#add-state-and-options) · [API guide](docs/usage.md) ·
[Contributing](CONTRIBUTING.md)

## Quick start

Install the tool and scaffold a runnable project:

```sh
go install github.com/sambhav/gobridge/cmd/gobridge@latest
gobridge init --dir greeter --module example.com/greeter --name acme.greeter
cd greeter
gobridge generate --dir bridge
go mod tidy
gobridge dev -- python app.py
# TypeScript: gobridge dev --typescript -- node app.mts
```

Use `--npm-package @acme/greeter` with `init` for an npm scope. Dotted Python
names such as `acme.tools.greeter` create native namespace packages.
[Packaging and development guide](docs/packaging.md) covers scaffolding,
wrappers, assets, dependencies, and live reload.

### Add gobridge manually

For an existing library, the equivalent setup is below. Start a new Go module
and install the tool:

```sh
mkdir greeter
cd greeter
go mod init example.com/greeter
go get github.com/sambhav/gobridge@latest
go install github.com/sambhav/gobridge/cmd/gobridge@latest
```

Put Go's binary directory on `PATH`. You need Go 1.23+ and Python 3.10+ for this
walkthrough. Generated packages include the runtime and Go executable; consumers
do not need Go or a separate gobridge runtime package.

Create `greeter.go`:

```go
package greeter

//gobridge:export
func Greet(name string) string { return "Hello, " + name + "!" }
```

Create `cmd/greeter/main.go`:

```go
package main

import greeter "example.com/greeter"

func main() {
    registry, err := greeter.NewGobridge()
    if err != nil { panic(err) }
    registry.Main()
}
```

Create `gobridge.json`:

```json
{
  "name": "greeter",
  "source": ".",
  "command": "./cmd/greeter",
  "version": "0.1.0"
}
```

Create `app.py`:

```python
import asyncio
from greeter import greet

async def main():
    print(await greet(name="World"))

asyncio.run(main())
```

Run it with live rebuilds:

```sh
gobridge dev -- python app.py
```

This generates the Go adapter, builds the binary and writes the complete Python
package into `build/greeter`. `--once` builds without watching or running an app.
In an async application or notebook, use its existing event loop. Imports start
no process; calls reuse one private Go daemon, cleaned up at process exit.

The same function is also a CLI:

```sh
go run ./cmd/greeter greet --name World
```

CLI calls print JSON to stdout and diagnostics to stderr. Try `greet --help`,
`greet --json '{"name":"World"}'`, or `--json -` to read stdin.

Python functions are async and keyword-only. Synchronous scripts can import
`greet_sync` instead:

```python
from greeter import greet_sync

print(greet_sync(name="World"))
```

## Build and install packages

From your Go project, inspect the plan with `gobridge build --check`, then build
the formats you need:

```sh
gobridge build --python
gobridge build --typescript
# Or build both:
gobridge build --python --typescript
```

The command reads `gobridge.json`, generates bindings, cross-compiles Go, and
bundles the executable and private runtime. Artifacts are staged and accompanied
by a checksum manifest; use `--replace` to overwrite different same-version
artifacts. Python wheels need only Python's
standard library to build. TypeScript packaging also needs Node 24+ and npm.

```sh
# Install the packages built above:
pip install --no-index --find-links dist greeter
npm install ./dist/npm/greeter-0.1.0.tgz
```

```ts
import { greet } from "greeter";

console.log(await greet({ name: "World" }));
```

All six Linux/macOS/Windows × amd64/arm64 targets build by default with
`CGO_ENABLED=0`. Use `--targets linux-amd64,darwin-arm64` for fewer targets,
`--output` for another directory, and `--version 0.2.0` to override the manifest.
Libraries requiring cgo need their own target build recipe.

Publish `dist/*.whl` to PyPI with Twine or `dist/npm/*.tgz` with `npm publish`.
Optional manifest fields `python_distribution` and `npm_package` set registry
names independently of the Python import name, for example `acme-greeter` and
`@acme/greeter`. `repository` and `license` set package metadata. Consumers install
your package; gobridge's internal runtimes and examples are not published separately.

For an organization-owned package, keep the names explicit:

| Setting | Example | Used by |
| --- | --- | --- |
| `name` | `acme.greeter` | Python import path; executable is `acme_greeter` |
| `python_distribution` | `acme-greeter` | `pip install acme-greeter` |
| `npm_package` | `@acme/greeter` | `npm install @acme/greeter` and TypeScript imports |
| `class` | `Greeter` | Python `Greeter`/`SyncGreeter` and TypeScript `Greeter` |

See the [packaging guide](docs/packaging.md) for a complete manifest, generated
package layouts, local installation, publishing, and TypeScript namespace imports.
Dotted names such as `acme.greeter` generate native Python namespace packages:
`from acme.greeter import greet`. Both wheels and dev mode support them.

## Add state and options

Add the following to `greeter.go` to introduce constructor options:

```go
type Options struct {
    Prefix *string `json:"prefix,omitempty"`
}

type Greeter struct { prefix string }

//gobridge:constructor
func NewGreeter(options Options) *Greeter {
    prefix := "Welcome, "
    if options.Prefix != nil { prefix = *options.Prefix }
    return &Greeter{prefix: prefix}
}

//gobridge:export
func (g *Greeter) Welcome(name string) string { return g.prefix + name }
```

The dev command regenerates the Python constructor and methods. Each instance
owns an independent Go object and daemon:

```python
from greeter import Greeter

async with Greeter(prefix="Hey, ") as greeter:
    print(await greeter.welcome(name="Sam"))  # Hey, Sam
```

Results are normal scalars or frozen dataclasses. Void Go methods return `None`.
There is no required Pydantic dependency. Keep a client alive for useful work;
creating a process per call is expensive.

Synchronous applications use `SyncGreeter` and ordinary `with`:

```python
from greeter import SyncGreeter

with SyncGreeter(prefix="Hey, ") as greeter:
    print(greeter.welcome(name="Sam"))
```

The CLI supplies these options with `--config '{"prefix":"Hey, "}'` before
`welcome --name Sam`. Defaults stay in your Go constructor.

## Supported APIs

Export functions and methods with `//gobridge:export`; mark an optional constructor
with `//gobridge:constructor`. Names become snake_case in Python and camelCase in
TypeScript. Leading `context.Context` parameters receive cancellation and deadlines.
Go errors become exceptions. Only annotated declarations are exposed.

Values can be strings, booleans, signed integers, finite floats, named structs,
slices, string-keyed maps, and pointers. Struct fields need explicit `json` names.
Python returns frozen dataclasses; TypeScript returns typed plain objects and uses
`bigint` for Go `int64`. Recursive types, interfaces, custom marshalers, variadic
functions, and multiple non-error results need adapters.

Use the [API guide](docs/usage.md) for manual registration, validation, sessions,
timeouts, development reloads, and Cobra embedding. The runnable examples are
[greeter](examples/greeter/greeter.go) and [Cobra](examples/cobra/README.md).
For tests, benchmarks, and releasing gobridge itself, see
[Contributing](CONTRIBUTING.md).
