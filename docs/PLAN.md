# Implementation plan

## Phase 1: Go, CLI and Python — this repository

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

These checks denote implemented code, not a released compatibility guarantee.
CI and the validation notes record which environments were actually tested.

## Phase 2: harden the release boundary

1. Run the CI matrix on real Linux, macOS and Windows; validate arm64 hosts.
2. Add protocol fuzzing, long-running stress/soak tests, process-kill matrices,
   benchmarks and memory bounds under cancellation storms.
3. Audit all inherited resource paths with platform experts; document supported
   Python runtimes, especially free-threaded CPython and native extensions.
4. Certify Linux wheel tags, sign/checksum release artifacts, test installation
   on clean hosts, and automate reproducible versioned releases. Preserve the repository’s Apache-2.0 license in distributions.
5. Stabilize schema evolution: today exact fingerprints require regeneration,
   even for additive/documentation changes. Plan a compatibility negotiation
   policy before committing to long-term version guarantees.
6. Keep dataclasses as the dependency-free default. Consider an optional Pydantic
   generator mode only after measuring validation and serialization costs.
7. Add operation help/defaults/validation metadata and richer Python validation
   only when it has one source of truth in the Go schema.

## Phase 3: TypeScript

Use the same versioned protocol and schema. Generate a client class with typed
parameter objects, result interfaces and Promise-returning methods. Map
AbortSignal to cancellation. Each Node process owns its daemon; explicitly
share a client within an event loop. Worker threads start separate clients by
default, with an opt-in broker only if needed. Browser runtimes cannot launch
local processes and are outside this transport's scope.

Resolve integer semantics before shipping: Go signed 64-bit values can exceed
JavaScript's safe Number range. Define tagged decimal-string bigint fields or
restrict the schema; never silently round. Python preserves JSON integer values.

## Phase 4: opt-in richer resource models

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

A user can install a platform wheel into a clean Python environment, instantiate
a generated class without finding a binary, use threads and asyncio, pass a
client configuration into multiprocessing, receive useful errors/cancellation,
and close everything without leaking daemon processes. The same Go operations
work through the CLI. Documented OS tests and packaging gates must pass before
tagging that release.
