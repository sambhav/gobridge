# API evolution: simple functions, configured objects, explicit control

**Implementation status.** This design was committed before implementation.
The Python convenience increment adds generated module functions, `aio`, lazy
process-local defaults, explicit clients, `RuntimeOptions`, and scoped controls.
Their runnable example and integration tests live in [Hello World](HELLO_WORLD.md).
Direct Go function binding and constructor/method APIs are implemented too;
[the annotated example](../examples/annotated/main.go) generates their adapters
from ordinary Go declarations.

## 1. Import a function and call it

```python
from hello import greet

print(greet(name="World").message)
```

Importing must not start a process. The first call lazily starts a private
Go daemon; subsequent module calls reuse it. The default daemon is **local to
the Python process and generated module**, not global to the machine.
Different applications never accidentally share caches or credentials.

```python
from hello import aio

greeting = await aio.greet(name="Async")
```

Sync and async module functions use the same default client/session. Async calls
must not inherit a transport owned by a previous event loop. New multiprocessing
workers get fresh connections and Go state; they never use the parent's pipes.

## 2. Explicit instances provide isolated state

```python
from calculator import Calculator

with Calculator(initial=10, prefix="answer: ") as calculator:
    result = calculator.add(value=5)
```

Go constructor configuration becomes typed Python keyword arguments. Each
explicit client owns one daemon and one Go object. Threads/tasks may share the
client intentionally; unrelated instances are isolated. Closure is explicit
with context managers; closing an instance never silently reopens it.

Keep transport configuration separate from domain options:

```python
from gobridge import RuntimeOptions

with Calculator(initial=10, prefix="", _runtime=RuntimeOptions(
    command="./bin/calculator", timeout=5, max_pending=64,
)) as calculator:
    result = calculator.add(value=2)
```

Generated classes may retain a positional binary override for development, but
normal wheel users should see only their library's constructor arguments.
Go pointer options become optional Python keywords defaulting to `None`.
Go zero values are not silently guessed as Python defaults for required fields.

## 3. Lifecycle and scoped overrides live behind `control`

```python
import hello

# Optional eager startup; ordinary calls do this lazily.
hello.control.start()
hello.control.close()

# Inside this scope, module functions use an isolated client.
with hello.control.scope() as client:
    greeting = hello.greet(name="Scoped")
    assert greeting == client.greet(name="Scoped")
```

Scopes should use ContextVar, so unrelated async tasks do not switch each
other's client. Child async tasks inherit Python's context as usual. New OS
threads do not automatically inherit it: pass an explicit client to threads
when you need shared scoped state. Nested scopes restore the previous client.
Async scopes use `async with` and perform startup/shutdown off the event loop.

Closing the default via `control.close()` releases it; a later module call may
create a fresh default. This is a deliberate reset, never an automatic retry
after a crash. Calling a dead default raises until the user explicitly resets.

For a library whose default requires constructor options,
`control.configure(...)` supplies them before the default is created. It must
reject replacing a live default's configuration. Explicit instances remain the
recommended way to manage different accounts, endpoints or option sets.

The runnable Hello example only needs an executable override during development:

```python
from hello import control, greet

control.configure(command="./bin/hello")
print(greet(name="Development").message)
control.close()
```

Installed wheels resolve their package-data binary without that configuration.

## 4. Expose existing Go functions without boilerplate DTOs

```go
func Greet(name string) string { return "Hello, " + name + "!" }

r := bridge.New()
if err := bridge.Bind(r, "greet", Greet, "name"); err != nil { panic(err) }
r.Main()
```

Explicit parameter names are necessary: runtime Go reflection does not retain
source parameter names. Supported signatures include an optional leading
context.Context, typed parameters, and either a value, value+error, error only,
or no result. Generated Python functions return the corresponding scalar/model
or None. Unsupported signatures fail during registration.

## 5. Constructors and methods preserve Go object ownership

```go
type Options struct {
    Initial int `json:"initial"`
    Prefix string `json:"prefix"`
}

func NewCalculator(options Options) (*Calculator, error) {
    // Adapt existing Go functional options in one place.
    return New(WithInitial(options.Initial), WithPrefix(options.Prefix))
}

r := bridge.New()
object, err := bridge.NewObject(r, NewCalculator)
if err != nil { panic(err) }
if err := object.Bind("add", (*Calculator).Add, "value"); err != nil { panic(err) }
r.Main()
```

Method expressions provide the signature without constructing an object.
Schema generation must never execute a constructor, open a network connection
or require credentials. Python sends initialization once after the protocol
handshake and before exposing the client to calls. Initialization failure must
reap the child. One constructor/object per daemon is enough for this increment.

Go option functions remain in Go; their serializable configuration is a named
options struct. Opaque closures cannot cross the language boundary. Mutable
receivers still need synchronization appropriate to the underlying library.
The bridge cannot make an arbitrary Go object thread-safe merely by exporting it.

## Performance and safety rules

- Dataclasses and standard-library transport by default; no mandatory Pydantic.
- Reuse processes and schemas; avoid per-call reflection/type-hint resolution
  and unnecessary thread-pool hops on Python's warm async path.
- Keep default-client creation lazy, thread-safe and process-owned.
- Never silently share across processes, replay failed operations, replace live
  configuration, or drop Go state by restarting a daemon behind the user's back.
- Keep backpressure, bounded requests, cancellation and explicit shutdown.
- Test public examples, constructor failure, repeated event loops, nested scopes,
  concurrent default initialization, fork/spawn and clean wheel installs in CI.

See [Hello World](HELLO_WORLD.md), [Go API](GO_API.md),
[architecture](ARCHITECTURE.md) and [performance measurements](PERFORMANCE.md).
