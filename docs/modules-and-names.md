# Modules, names, and functional options

A distribution can contain several bridge modules. Each module has its own Go
command, generated clients, bundled executable, and lazy default client. Python
import paths, npm export paths, distribution names, and client class names are
independent. Two modules can wrap the same Go command with different public APIs.

```json
{
  "version": "1.0.0",
  "python": {"distribution": "acme-sdk"},
  "typescript": {"package": "@acme/sdk"},
  "modules": [
    {
      "name": "catalog",
      "source": "./catalog",
      "command": "./cmd/catalog",
      "python": {
        "module": "acme.catalog",
        "class": "CatalogClient",
        "rename": {
          "operations": {"lookup": "find"},
          "types": {"Result": "Item"},
          "fields": {"Result.item_id": "id"}
        }
      },
      "typescript": {
        "export": "./catalog",
        "class": "CatalogClient",
        "rename": {"operations": {"lookup": "find"}}
      }
    },
    {
      "name": "text",
      "source": "./text",
      "command": "./cmd/text",
      "python": {"module": "acme.text"},
      "typescript": {"export": "./text"}
    }
  ]
}
```

`gobridge build --python --typescript` produces **one wheel per target** and
**one npm tarball** containing all the modules. Consumers import `acme.catalog`
and `acme.text`, or `@acme/sdk/catalog` and `@acme/sdk/text`. Use the npm export
`.` for a root entrypoint. Parent Python namespaces use PEP 420 unless another
configured module supplies their initializer.

`name` identifies a module for tooling. Python's `module` defaults to that name;
the npm `export` defaults to `./` followed by the name with dots changed to
slashes. `source` is optional for manually registered commands. Existing flat
single-module configurations still work. Put source, command, class, and package
additions inside `modules` when using the new form; do not mix nested distribution
settings with their legacy flat equivalents.

Distribution-level `python.requires` and `typescript.dependencies` contain
application dependencies. A module's `python.package` or `typescript.package`
points to handwritten package additions, with the same rules as the legacy
`python_package` and `typescript_package` settings. Dependencies are shared by
the distribution; generated runtimes and binaries remain private to each module.

For development, select one module:

```sh
gobridge dev --module catalog --once
gobridge dev --module catalog -- python app.py
gobridge dev --module catalog --typescript -- node app.mjs
```

Python development outputs the selected import path under `build`. TypeScript
development installs the selected module's configured export in the local npm
package. Use `build` to package every module together; selecting another module
for TypeScript development replaces that development package.

## Names in Go source

```go
//gobridge:python Product
//gobridge:ts ProductRecord
type Item struct {
    ItemID string `json:"item_id" python:"id" ts:"productID"`
}

//gobridge:export lookup
//gobridge:python find
//gobridge:ts findProduct
func Lookup(itemID string) Item { /* ... */ }
```

`//gobridge:export` determines the wire operation name. `json` tags determine wire
field names. Language annotations and `python`/`ts` tags affect generated public
names only; encoding and decoding preserve the Go JSON contract, including
nested structs and containers. Put language annotations on a constructor or its
receiver struct to name the generated client class. Put them on other model
structs to name generated dataclasses and TypeScript interfaces.

Module rename maps override source annotations and tags. Operation map keys are
wire names. Type keys are Go type names, optionally qualified as
`example.com/project/catalog.Item`. Field keys use `Item.item_id` or `Item.ItemID`,
also optionally qualified. Bound function parameters use the generated input
model name, such as `LookupParams.item_id`. Unknown map keys, empty overrides,
invalid identifiers, reserved names, and public-name collisions fail generation.
Renaming does not change the wire schema hash.

Manual registries and generated adapters support composable Go options:

```go
r := gobridge.New(
    gobridge.WithPython(gobridge.Names{
        Class: "CatalogClient",
        Operations: map[string]string{"lookup": "find"},
    }),
)

// The generated adapter accepts the same options, overriding annotations.
r, err := catalog.NewGobridge(
    gobridge.WithTypeScript(gobridge.Names{
        Types: map[string]string{"Item": "Product"},
    }),
)
```

Options merge left to right and copy their maps. Per-generation options can also
be passed to `GeneratePython` and `GenerateTypeScript`; these override registry
options without mutating the registry. A configured language class overrides
the generator's fallback class argument. No separate builder is needed.

## Exporting Go functional options as constructor keywords

Annotate the constructor and the option factories you want to expose:

```go
type Option func(*Catalog)

//gobridge:constructor
func New(options ...Option) *Catalog { /* apply defaults and options */ }

//gobridge:option endpoint
//gobridge:python base_url
//gobridge:ts baseURL
func WithEndpoint(endpoint string) Option { /* ... */ }

//gobridge:option retries
func WithRetries(retries int) (Option, error) { /* ... */ }
```

The resulting API uses native constructor arguments:

```python
async with Catalog(base_url="https://api.example", retries=0) as catalog:
    result = await catalog.lookup(item_id="123")
```

```typescript
const catalog = new Catalog({baseURL: "https://api.example", retries: 0});
try {
  const result = await catalog.lookup({itemId: "123"});
} finally {
  await catalog.close();
}
```

Omission (or Python `None`/JSON `null`) skips that factory and preserves the Go
default. Zero, false, and empty strings are explicit values. Factories run in
source declaration order (files sorted by name), regardless of keyword order.
Neither factories nor the constructor run while generating schemas or bindings.
Factories and constructors may return errors; those errors reach the client.

Supported constructors take `...Option`, optionally preceded by
`context.Context`, and return `*T` or `(*T, error)`. Factories return `Option` or `(Option, error)`. A single parameter stays a scalar
(or its existing wire model). Multiple parameters generate a grouped model:

```go
//gobridge:option retry
func WithRetry(attempts int, delayMs int) (Option, error) { /* ... */ }
```

```python
Catalog(retry=RetryOptions(attempts=3, delay_ms=100))
```

```typescript
new Catalog({retry: {attempts: 3, delayMs: 100}})
```

The generator reads parameter names from Go declarations. The group is optional;
inside a supplied group, non-pointer parameters are required, including explicit
zero/false values. Pointer parameters retain the usual nullable/optional behavior.
Values are passed to the factory in Go declaration order. Generated group names
use the option's wire name (`retry` becomes `RetryOptions`) and support the same
module type/field rename maps, e.g. `RetryOptions.delay_ms`. Use a slice for
repeated values. Arbitrary callbacks, overloaded options, and fluent builder methods need
an explicit Go adapter. Pointer-valued options cannot distinguish an omitted
keyword from explicitly passing null.

Manual registration uses the same adapter:

```go
object, err := gobridge.NewObjectOptions(r, New,
    gobridge.ConstructorOption("endpoint", WithEndpoint),
    gobridge.ConstructorOption("retries", WithRetries),
    gobridge.ConstructorOption("retry", WithRetry, "attempts", "delay_ms"),
)
```

The existing `NewObject(r, func(Config) *T)` API remains available. See the
[complete catalog example](../examples/catalog/catalog.go) for executable code.
