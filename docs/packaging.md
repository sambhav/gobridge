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
  "name": "acme_greeter",
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
| `name` | Python import package and executable filename. One lowercase identifier, with underscores allowed; no dots, hyphens, Python keywords, or `gobridge`. |
| `class` | Generated client class; defaults to PascalCase derived from `name`, so `acme_greeter` becomes `AcmeGreeter`. Set `Greeter` explicitly to keep the shorter API. |
| `source` | Annotated Go library directory, relative to the project root. Omit for a manually registered registry. |
| `command` | Go command package to compile. The directory need not match the executable's generated name. |
| `version` | Your application package version; defaults to `0.1.0`. Use a stable `X.Y.Z`; prereleases are not currently supported by the wheel builder. |
| `python_distribution` | Name used by pip/PyPI; defaults to `name` with underscores replaced by hyphens. |
| `npm_package` | Name used by npm and JS/TS imports; defaults to `name` with underscores replaced by hyphens. May include an `@scope/` prefix. |
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
remains `acme-greeter`, while the import is `acme_greeter`.
See Python's [distribution versus import package explanation](https://packaging.python.org/en/latest/discussions/distribution-package-vs-import-package/).

Save this as `app.py` and run `python app.py`:

```python
import asyncio
from acme_greeter import greet

async def main():
    print(await greet(name="World"))

asyncio.run(main())
```

For synchronous code, use `from acme_greeter import greet_sync`. If you added the
README's stateful Go constructor, you can also use:

```python
from acme_greeter import SyncGreeter

with SyncGreeter(prefix="Hello, ") as client:
    print(client.welcome(name="World"))
```

The wheel contains:

| Installed path | Purpose |
| --- | --- |
| `acme_greeter/__init__.py` | Generated typed functions, clients, and result classes |
| `acme_greeter/py.typed` | Marker for type checkers and editors |
| `acme_greeter/_gobridge/` | Private transport runtime and its license |
| `acme_greeter/_bin/acme_greeter` | Executable for this wheel's platform; `.exe` on Windows |
| `acme_greeter-0.1.0.dist-info/` | Distribution metadata, wheel tags, file records, and application license when provided |

Consumers need Python 3.10+ only. The binary is located relative to the installed
package; it does not need to be on `PATH`. Imports do not start a process.
No separate gobridge Python package or Pydantic dependency is required.
The builder supplies the wheel metadata and uses your root `README.md` as its
description; you do not need a handwritten `pyproject.toml` for this workflow.

### Python namespaces

`acme_greeter` provides an organization-prefixed import name. It is a regular
Python package, not a PEP 420 namespace package. Setting the distribution to
`acme-greeter` or `acme.greeter` does not create `from acme.greeter import greet`.
The CLI currently rejects `"name": "acme.greeter"`; there is no `namespace` field.

If a shared `acme.*` namespace is a hard requirement, you need a custom packaging
pipeline or namespace support added to the builder. A native namespace layout
would put the complete generated package at `acme/greeter/` and omit
`acme/__init__.py`; its bindings, private runtime, typing marker, and binary must
stay together. Renaming an already-built wheel is insufficient: its contents and
`RECORD` also need to describe the correct paths. Follow the
[Python namespace packaging guide](https://packaging.python.org/guides/packaging-namespace-packages/)
for that custom layout. The commands on this page use the supported flat package.

### Local Python development

With the same manifest and `app.py` above:

```sh
gobridge dev -- python app.py
```

The dev command generates `build/acme_greeter`, adds `build` to the application's
`PYTHONPATH`, and rebuilds/restarts on source changes. A pip install is unnecessary
in this loop. Use `gobridge dev --once` to generate without running a watcher;
your own application launcher then needs `build` on its Python import path.

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
application. Do not point TypeScript consumers at `build/acme_greeter`.

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
