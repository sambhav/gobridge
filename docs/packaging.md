# Packaging Python and TypeScript libraries

Start with the [README's Go library and command](../README.md#quick-start).
Run the following commands from that Go project's root. The names below are
examples: substitute your own organization and package names before publishing.

## Choose the names independently

The Go import path, Python import name, PyPI distribution name, and npm package
name serve different purposes. They do not need to match. For example, the Go
library can remain `package greeter` in the module `example.com/greeter` while
its generated packages use your organization's prefix or scope.

Replace `gobridge.json` with:

```json
{
  "name": "acme.greeter",
  "class": "Greeter",
  "source": ".",
  "command": "./cmd/greeter",
  "version": "0.1.0",
  "python_distribution": "acme-greeter",
  "npm_package": "@acme/greeter",
  "repository": "https://github.com/acme/greeter",
  "license": "Apache-2.0"
}
```

| Field | Meaning and default |
| --- | --- |
| `name` | Python import path: dot-separated lowercase identifiers with underscores allowed. Namespace parents have no `__init__.py`. The executable replaces dots with underscores. No hyphens, Python keywords, or top-level `gobridge`. |
| `class` | Generated client class; defaults to PascalCase from the last component, so `acme.greeter` becomes `Greeter` and `acme.text_kit` becomes `TextKit`. Flat `acme_greeter` still becomes `AcmeGreeter`. |
| `source` | Annotated Go library directory, relative to the project root. Omit for a manually registered registry. |
| `command` | Go command package to compile. The directory need not match the executable's generated name. |
| `version` | Your application package version; defaults to `0.1.0`. Use a stable `X.Y.Z`; prereleases are not currently supported by the wheel builder. |
| `python_distribution` | Name used by pip/PyPI; defaults to `name` with dots and underscores replaced by hyphens. |
| `npm_package` | Name used by npm and JS/TS imports; defaults to `name` with dots and underscores replaced by hyphens. May include an `@scope/` prefix. |
| `repository` | Optional source repository URL included in package metadata. |
| `license` | Optional application license identifier. Include your actual `LICENSE` file in the project root too. |

The Python client names are `Greeter` and `SyncGreeter`; TypeScript exports
`Greeter`. Operation names are derived from Go declarations independently of the
package names. Changing `name` changes Python imports and the bundled executable;
changing only `python_distribution` does not change imports. Changing `npm_package`
changes both npm installation and JS/TS import specifiers.

## Build and use the Python package

```sh
gobridge build --python
python -m pip install --no-index --find-links dist acme-greeter
```

This generates one wheel per selected platform. For example, the Linux amd64
wheel is named:

```text
acme_greeter-0.1.0-py3-none-manylinux_2_17_x86_64.musllinux_1_2_x86_64.whl
```

The underscores in the filename are wheel name normalization. The install name
remains `acme-greeter`, while the import is `acme.greeter`.
See Python's [distribution versus import package explanation](https://packaging.python.org/en/latest/discussions/distribution-package-vs-import-package/).

Save this as `app.py` and run `python app.py`:

```python
import asyncio
from acme.greeter import greet

async def main():
    print(await greet(name="World"))

asyncio.run(main())
```

For synchronous code, use `from acme.greeter import greet_sync`. If you added the
README's stateful Go constructor, you can also use:

```python
from acme.greeter import SyncGreeter

with SyncGreeter(prefix="Hello, ") as client:
    print(client.welcome(name="World"))
```

The wheel contains:

| Installed path | Purpose |
| --- | --- |
| `acme/greeter/__init__.py` | Generated typed functions, clients, and result classes |
| `acme/greeter/py.typed` | Marker for type checkers and editors |
| `acme/greeter/_gobridge/` | Private transport runtime and its license |
| `acme/greeter/_bin/acme_greeter` | Executable for this wheel's platform; `.exe` on Windows |
| `acme_greeter-0.1.0.dist-info/` | Distribution metadata, wheel tags, file records, and application license when provided |

Consumers need Python 3.10+ only. The binary is located relative to the installed
package; it does not need to be on `PATH`. Imports do not start a process.
No separate gobridge Python package or Pydantic dependency is required.
The builder supplies the wheel metadata and uses your root `README.md` as its
description; you do not need a handwritten `pyproject.toml` for this workflow.

### Python namespaces

`"name": "acme.greeter"` creates a native PEP 420 namespace package. Only the
leaf `acme/greeter/` contains `__init__.py`, `py.typed`, the private runtime, and
the executable. The parent `acme/` deliberately has no `__init__.py` or
`py.typed`, allowing independently installed distributions to share it.

For another library, use `"name": "acme.textkit"` with
`"python_distribution": "acme-textkit"`. Consumers can install both distributions
and import `acme.greeter` and `acme.textkit`. Each leaf carries its own runtime and
binary; uninstalling one leaves the other intact. Deeper paths such as
`acme.tools.greeter` work the same way.

Choose distinct leaf paths for independent distributions. Do not install a
regular `acme` package containing `acme/__init__.py` alongside these namespace
packages: it can prevent namespace discovery across import locations. Dev mode
rejects existing regular-package parents rather than modifying handwritten files.
See the [Python namespace packaging guide](https://packaging.python.org/guides/packaging-namespace-packages/)
for how independently distributed namespace portions compose.

The distribution name remains independent: setting only
`"python_distribution": "acme-greeter"` does not change the import path.
`"name": "acme_greeter"` remains supported for a flat organization-prefixed package.

### Local Python development

With the same manifest and `app.py` above:

```sh
gobridge dev -- python app.py
```

The dev command generates `build/acme/greeter`, adds `build` to the application's
`PYTHONPATH`, and rebuilds/restarts on source changes. A pip install is unnecessary
in this loop. Use `gobridge dev --once` to generate without running a watcher;
your own application launcher then needs `build` on its Python import path.
For a custom output, use `--python generated/acme/greeter`; the path must end in
the complete namespace path. The watcher automatically adds `generated`, not
`generated/acme`, to `PYTHONPATH`.

## Build and use the TypeScript package

With Node 24+ and npm available:

```sh
gobridge build --typescript
npm install ./dist/npm/acme-greeter-0.1.0.tgz
```

The scoped name `@acme/greeter` becomes the tarball filename
`acme-greeter-0.1.0.tgz`. The installed package retains its full scope.

Use named imports:

```ts
import { greet } from "@acme/greeter";

console.log(await greet({ name: "World" }));
```

Or a TypeScript/JavaScript namespace import:

```ts
import * as greeter from "@acme/greeter";

console.log(await greeter.greet({ name: "World" }));
```

The npm scope is the registry namespace; `greeter` in the second example is a
local variable name you choose. Neither requires a TypeScript `namespace`
declaration. For the README's stateful constructor:

```ts
import { Greeter } from "@acme/greeter";

const client = new Greeter({ prefix: "Hello, " });
try {
  console.log(await client.welcome({ name: "World" }));
} finally {
  await client.close();
}
```

The package ships ESM JavaScript (`index.js`), type declarations (`index.d.ts`),
the private `_gobridge/` runtime, and `_bin/<platform>-<arch>/` executables. One
tarball contains every selected target; the runtime picks the host binary.
It has no production npm dependencies, and consumers do not compile Go.

Use an ESM application: `.mjs` for JavaScript or `.mts` for TypeScript, or set
`"type": "module"` in your application's `package.json`. For a Node TypeScript
project, install the development tools:

```sh
npm install --save-dev typescript @types/node
```

Save a snippet above as `app.mts` and use this `tsconfig.json`:

```json
{
  "compilerOptions": {
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "target": "ES2022",
    "lib": ["ESNext"],
    "types": ["node"],
    "strict": true,
    "outDir": "dist"
  },
  "include": ["*.mts"]
}
```

Run `npx tsc` followed by `node dist/app.mjs`. `ESNext` includes the
`AsyncDisposable` and `Symbol.asyncDispose` types referenced by the generated
client, even if your code uses `close()` instead of `await using`. `@types/node`
describes the Node environment; the generated library already includes its own
declarations and needs no package-specific `@types` installation. The generated
export is ESM; gobridge does not build a separate CommonJS entry point or a
browser bundle.

`gobridge dev` currently watches Python applications. For TypeScript development,
rebuild the tarball and reinstall it in your consumer project, then restart the
application. Do not point TypeScript consumers at `build/acme/greeter`.

## Targets, versions, and publication

Both formats can be built together:

```sh
gobridge build --python --typescript --version 0.2.0 --output dist/0.2.0
```

All six targets are selected by default: `linux-amd64`, `linux-arm64`,
`darwin-amd64`, `darwin-arm64`, `windows-amd64`, and `windows-arm64`.
For a faster Linux amd64 development build, append `--targets linux-amd64`.
The target must include the machine where you will install and run the package.
Cross-compilation uses `CGO_ENABLED=0`; cgo libraries need a custom build recipe.

`--version` overrides the manifest for that build without editing it. Your
application's version is independent of the gobridge module version. The runtime
is copied from the gobridge version selected by your Go module, so rebuild your
packages after upgrading that dependency.

After testing the artifacts, publish your own packages. For the `0.1.0` build
above, with registry credentials and permission to publish the chosen names:

```sh
python -m pip install twine
python -m twine check --strict dist/*.whl
python -m twine upload dist/*.whl

npm publish ./dist/npm/acme-greeter-0.1.0.tgz --access public
```

Publish every supported wheel so pip can select the right platform. Use a clean
output directory per release to avoid uploading older versions. Publish the npm
tarball rather than running `npm publish` in the Go project root.
You must own or have publishing access to the npm scope; `--access public` makes
the scoped package public. See npm's
[scoped package publishing guide](https://docs.npmjs.com/creating-and-publishing-scoped-public-packages/).

These commands publish your application. `gobridge build` itself never publishes,
and gobridge's release workflow does not publish downstream authors' packages.

## Scaffold a project

```sh
gobridge init --dir greeter --module example.com/greeter \
  --name acme.tools.greeter --npm-package @acme/greeter
cd greeter
gobridge generate --dir bridge
go mod tidy
gobridge dev -- python app.py
```

`init` writes an editable annotated Go library in `bridge/`, its command in
`cmd/bridge/`, a manifest, and Python/TypeScript applications. An existing
`go.mod` supplies the module path; a new module requires `--module`.
`init --dry-run` prints the planned file contents. Existing files are never
replaced. The generated module pins the installed CLI's version.

For TypeScript, use `gobridge dev --typescript -- node app.mts`. Node 24+ runs
this example directly. For IDE checking or a compiled application, use the
TypeScript configuration earlier in this guide. Dev installs its own compiler
in the generated package; your application's compiler remains yours to configure.

## Inspect and publish local build output

```sh
gobridge build --python --typescript --targets linux-amd64 --check
gobridge build --python --typescript --targets linux-amd64
gobridge build --python --typescript --targets linux-amd64 --replace
```

`--check` (also `--dry-run`) prints a JSON plan to stdout, including resolved names,
languages, targets, artifact filenames, destination, and tool versions.
Use `gobridge build --check > build-plan.json` to save it. It validates configuration
before generating adapters or creating build output. Python-only builds do not
require Node. Inspection checks the main package and toolchain; it does not
promise that application code will compile.

Builds finish all selected formats and targets in staging first. Publication
replaces individual artifacts atomically and writes `gobridge-build.json` last.
The completion manifest lists the artifacts from that invocation with SHA-256
checksums and sizes. Other versions already in the directory are retained.
Consumers of automated builds should use the manifest, rather than infer a
complete build from a directory listing.

Existing artifacts with different bytes cause an error unless `--replace` is
explicit. Ordinary publication failures restore previous files. Concurrent
builders cannot publish into the same directory. A forcibly terminated process
may leave a `.gobridge-build-lock` and staging directory; remove them only after
confirming no builder is running, then rebuild. There is no automatic registry
publication.

## Add wrappers, assets, and dependencies

Add only the package customization fields you need:

```json
{
  "name": "acme.tools.greeter",
  "source": "./bridge",
  "command": "./cmd/bridge",
  "npm_package": "@acme/greeter",
  "python_package": "python-package",
  "typescript_package": "typescript-package",
  "python_requires": ["typing-extensions>=4"],
  "npm_dependencies": {"some-library": "^1.0.0"}
}
```

Use actual dependencies required by your wrappers; neither example dependency
is required by gobridge itself. Python requirements support package names,
extras, and version comparisons. npm dependencies use ordinary package specs.
Your consumers install these dependencies normally; dev does not install wrapper
dependencies into your Python environment or application.

The source directories must be inside the project. Their contents are copied
into the Python leaf package or npm package, including data assets. Namespace
parents remain empty PEP 420 namespaces. Symlinks, hidden files, and reserved
runtime/generated paths are rejected; there are no build hooks.

In a customized Python package, generated APIs live in `_bindings.py`. Create
`python-package/__init__.py` to define your public API:

```python
from ._bindings import greet_sync

def friendly(name: str) -> str:
    return greet_sync(name=name)
```

Use `importlib.resources.files(__package__)` for packaged data. If you omit
`__init__.py`, gobridge re-exports the generated API. Both dev and wheel builds
support these wrappers; development packages keep complete immutable revisions.

In a customized TypeScript package, generated APIs live in `generated.ts`.
Create `typescript-package/index.ts`:

```typescript
export * from "./generated.js";
import { greet } from "./generated.js";

export async function friendly(name: string): Promise<string> {
  return greet({ name });
}
```

The build compiles your `.ts` files and emits declarations alongside JavaScript.
If you omit `index.ts`, it re-exports the generated API. Read packaged assets
relative to `import.meta.url`. The package exports its root entrypoint; additional
public APIs should be re-exported there. The project's README and license are
included in the package.

## Development watching

Python: `gobridge dev -- python app.py`.
TypeScript: `gobridge dev --typescript -- node app.mts`.
Use `--once` without an application to generate just one development revision.

TypeScript packages live in `node_modules/<npm_package>`, including scope
directories. Dev refuses to take over an existing installed or handwritten
package. Remove that package deliberately before switching it to dev ownership.
No npm tarball or repeated install is needed when source changes.

Go source and `go:embed` asset additions, edits, and removals rebuild the package.
Application `.py`, `.ts`, `.mts`, `.js`, `.mjs`, `.cts`, and `.cjs` edits restart
the application without rebuilding Go. Wrapper package changes regenerate the
package. Failed builds leave the last working package and application running.
Old imported revisions keep their matching runtime and executable.

Changing `gobridge.json` prints a restart-required message and pauses reloads;
restart dev to validate and apply the new configuration. Generated sibling
packages and common build/dependency directories are excluded. Watching is
limited to the project directory: changes in external local `replace` modules
require restarting dev. Invoke your own TypeScript compiler/watch runner in the
application command if your application needs transpilation beyond Node's native
TypeScript support.
