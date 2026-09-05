# Exposing Go libraries

This document describes the next API increment. `Register` is available now;
`Bind` and object constructors below are the implementation target for this PR.
See [API design](API_DESIGN.md) for the matching Python experience.

## Ordinary functions

Most library functions should not need request and response wrapper structs:

```go
func Add(left, right int) int { return left + right }

func main() {
    app := gobridge.New()
    if err := gobridge.Bind(app, "add", Add, "left", "right"); err != nil {
        panic(err)
    }
    app.Main()
}
```

The parameter names are explicit because Go reflection does not retain them.
They become CLI flags and concrete, typed Python keyword arguments:

```console
calculator add --left 2 --right 3
5
```

```python
with Calculator() as calculator:
    assert calculator.add(left=2, right=3) == 5
```

`Bind` accepts an optional leading `context.Context`. The supported return
signatures are `T`, `(T, error)`, `error`, and no return values. A void function
returns `None` in Python. Error codes created by `gobridge.Failure` survive the
boundary. Panics become a generic internal error without leaking panic text.

Wire values support strings, booleans, signed integers, floats, named structs,
slices, string-keyed maps, and pointers to those types. Struct fields need
explicit `json` names. Pointer arguments are optional and default to `None` in
Python. A slice argument is still required, even though a nil slice can cross as
`None`. Custom marshalers, recursive types, arbitrary interfaces, variadic
functions, and multiple non-error return values need explicit adapters.

Registration checks signatures before the daemon starts. `Bind` uses reflection
to adapt ordinary functions; `Register` keeps its direct typed call path for
applications that already have request and response types or want to avoid
reflection in the Go invocation itself. Process transport and JSON costs apply
to both paths.

## Methods and state

A bound method can be registered exactly like a function:

```go
counter := &Counter{}
if err := gobridge.Bind(app, "increment", counter.Increment, "amount"); err != nil {
    panic(err)
}
```

That receiver is shared by concurrent calls in the daemon. Protect mutable Go
state with the same mutexes, atomic operations, or concurrency-safe structures
you would use when calling the library from goroutines. A single synchronous
Python client may be called from several threads; async callers can issue
several operations at once. Binding a receiver does not implicitly serialize it.

Each explicitly created Python client owns its daemon. Different clients and
different Python processes therefore have separate receivers and in-memory
caches. A child Python process gets a fresh daemon; it does not inherit a live
parent protocol connection or a snapshot of the parent's Go object state.

## Constructor options

The object API will expose a Go constructor as Python initialization and bind
method expressions without running the constructor during schema generation:

```go
type Options struct {
    Initial int     `json:"initial"`
    Label   *string `json:"label,omitempty"`
}

func NewCounter(opts Options) (*Counter, error) {
    // Translate wire options into existing Go functional options here.
    return counter.New(counter.WithInitial(opts.Initial))
}

object, err := gobridge.NewObject(app, NewCounter)
if err != nil {
    panic(err)
}
if err := object.Bind("increment", (*Counter).Increment, "amount"); err != nil {
    panic(err)
}
```

```python
with Counter(initial=10, label="requests") as counter:
    assert counter.increment(amount=2) == 12
```

The first version supports one constructor per registry. Plain functions can
coexist with object methods. Constructor options are a named Go struct; the
constructor may accept a leading context and may return an error. All options
are applied before ordinary requests begin. Object methods receive the created
receiver implicitly; a receiver or remote object ID never appears in their
Python signatures.

`(*Counter).Increment` is a method expression, not a method bound to an existing
instance. This allows the bridge to discover the signature before initialization
and attach the process-owned instance after the constructor succeeds.

## Keep adapters small and explicit

Use a tagged options struct to map an existing Go library's functional options
to Python initialization. Use ordinary adapters for specialized Go types such
as `time.Time`, `[]byte`, or interfaces whose representation is a product choice.
The bridge should infer routine type plumbing and leave those representation
choices visible to the author.
