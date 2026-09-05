# Implementation plan

## Implemented: Go, CLI and Python

- [x] Typed Go registry using generic registration and explicit adapters.
- [x] JSON CLI, scalar/container flags, schema discovery and stdio daemon.
- [x] Per-client/per-process ownership with thread and event-loop multiplexing.
- [x] Generated sync/async Python classes, methods and result dataclasses.
- [x] Stable errors, bounded admission/frames, deadlines and cancellation.
- [x] Protocol and schema compatibility checks.
- [x] Go TTL/LRU cache with cancellation-aware coalescing.
- [x] Pickle configuration and fork detachment without parent-daemon ownership.
- [x] Integration tests with actual daemons, race tests and cross-platform CI.
- [x] Reference wheel recipe bundling cross-compiled Go executables.
- [x] Source annotations generate adapters from ordinary functions and methods.
- [x] Typed constructor options, scalar/void results and process-owned objects.
- [x] Lazy importable functions, shared sync/async defaults and isolated scopes.
- [x] Constructor failure, malformed protocol and in-flight-fork regression tests.
- [x] Importable Go example library with a separate executable entrypoint.
- [x] Per-operation CLI help, constructor discovery and strict metadata arguments.
- [x] Shared Go field documentation/constraints in validation, CLI help and Python metadata.
- [x] Nested JSON field discovery and exact int64 constraint values in CLI help.

These checks denote implemented code, not a released compatibility guarantee.
CI and the validation notes record which environments were actually tested.

## Completed gate: Go, CLI, Python and embedding

- [x] Finish and test embedding inside an existing CLI, including Cobra, with
  clear stream/argument ownership and no unexpected exits or constructor calls.
- [x] Keep the complete Linux/macOS/Windows matrix green for the final combined
  changes, including native Go use, source generation, Python multiprocessing,
  metadata, help, and clean bundled-wheel installs.
- [x] Verify the user-facing examples and documentation against that revision.

Each feature is documented first, then implemented and pushed as its own
reviewable increment. All 15 jobs passed at `3411af4` before TypeScript began.

## Implemented: TypeScript

- [x] Dependency-free Node 24+ runtime using the same protocol and schema.
- [x] Generated Promise-returning functions/classes and readonly camelCase models.
- [x] Typed constructor options with separate transport controls.
- [x] Full Go int64 range as bigint, with exact JSON numeric literals.
- [x] Lazy module defaults, async scopes, cancellation and async disposal.
- [x] Bounded pending requests/output queues, sticky failures and daemon cleanup.
- [x] Real Go integration, strict type checks and worker isolation tests.
- [x] Six-target npm bundle and clean offline package-install checks.
- [x] Node 24/26 CI on Linux, macOS and Windows alongside the Go/Python jobs.

The [TypeScript guide](TYPESCRIPT.md) records the public API and packaging recipe.
Browser runtimes cannot launch local processes and remain outside this transport.
All 25 combined jobs passed at `ba2642b`, including both Node versions on all
three operating systems and clean npm/wheel installations.

## Release hardening

1. Add native arm64-host coverage beyond cross-compilation, plus sustained
   cancellation/load tests, protocol fuzzing, and process-kill matrices.
2. Document supported Python runtimes, especially free-threaded CPython and
   interactions with native extensions and inherited resources.
3. Certify Linux wheel tags, sign/checksum artifacts, and automate reproducible
   versioned releases with the repository's Apache-2.0 license included.
4. Define a schema compatibility policy before long-term version guarantees.
   Current exact fingerprints require regeneration after metadata changes too.
5. Add defaults or further validation only through one Go schema contract.
   Keep dataclasses dependency-free; optional adapters need a concrete use case
   and measured costs.

## Later: opt-in resource models

- Streaming with bounded flow control and backpressure, not unbounded events.
- Go object handles only when users need multiple stateful instances inside
  one daemon: typed factories, explicit close, session IDs and stale-handle errors.
  Handles must never silently migrate across processes or daemon restarts.
- Shared daemon mode for expensive cross-process caches only with explicit
  endpoint ownership, per-connection sessions, identity boundaries, idle shutdown
  and Unix/Windows transport adapters. It is a separate operating mode.
- Disk-backed caches require data-versioned keys, a locking model and recovery
  semantics. Keep mutable cache ownership out of Python.

## Definition of the first release

A user can import the native Go library, install a platform wheel into a clean
Python environment, call an imported function or instantiate a configured class
without finding a binary, use threads and asyncio, and pass client configuration
into multiprocessing. Errors and cancellation behave predictably, and shutdown
reaps daemons. The same Go operations work through the standalone or embedded
CLI. Documented OS tests and packaging gates must pass before tagging a release.
Node users can install a bundled npm package and use the corresponding typed
functions, configured classes, AbortSignal cancellation and isolated async scopes.
