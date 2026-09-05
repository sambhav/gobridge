# gobridge-runtime

The Node.js runtime used by generated Go library bindings. It has no production
dependencies and supports Node.js 24 or newer, with ESM JavaScript and TypeScript
declarations.

Generated packages depend on this runtime and provide the application-facing
functions, classes, constructor options, and packaged Go executable. Their normal
imports need no Go compiler or manual daemon startup.

The runtime provides private stdio transports, exact signed 64-bit integer
handling, timeouts and cancellation, and ownership controls. An explicit client
owns its daemon; module functions share a lazy default, and asynchronous scopes
provide temporary isolated clients.

For repository development:

```sh
npm ci --ignore-scripts
npm test
```

From the repository root, `python tools/build_npm.py` creates local runtime and
example tarballs in `dist/npm`; `python tools/test_npm.py` installs them into a
clean temporary Node project and exercises the packaged binary. These commands
do not publish packages.
