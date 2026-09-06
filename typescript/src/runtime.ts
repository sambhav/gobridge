import { spawn, type ChildProcess } from "node:child_process";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parseWire, stringifyWire } from "./codec.js";
import {
  AbortError, BridgeError, BusyError, ClosedError, DaemonError,
  RequestTimeout, errorFromWire,
} from "./errors.js";

const MAX_FRAME = 1024 * 1024;
const MAX_TIMEOUT = 86_400_000;

/** Generated, snapshotted call descriptor. Construction does not start a daemon. */
export class Call<T> {
  readonly #params: string;
  constructor(readonly method: string, params: unknown, readonly decode: (value: unknown) => T) {
    this.#params = stringifyWire(params);
  }
  wire(): {method:string; params:unknown} { return {method:this.method,params:parseWire(this.#params)}; }
}
export type BatchResult<T> = {result:T; error?:never} | {result:null; error:BridgeError};
type CallValue<C> = C extends Call<infer T> ? T : unknown;

export interface RuntimeOptions {
  readonly command?: string | readonly string[];
  readonly timeoutMs?: number;
  readonly startupTimeoutMs?: number;
  readonly maxPending?: number;
}

export interface ClientOptions {
  readonly timeoutMs?: number;
  readonly startupTimeoutMs?: number;
  readonly maxPending?: number;
  readonly expectedSchema?: string;
  readonly init?: unknown;
}

export interface CallOptions {
  readonly signal?: AbortSignal;
  readonly timeoutMs?: number;
}

/** Resolve package data first; spawn performs the ordinary PATH fallback. */
export function resolveBinary(moduleUrl: string, binary: string): string {
  const filename = binary + (process.platform === "win32" ? ".exe" : "");
  const bundled = join(dirname(fileURLToPath(moduleUrl)), "_bin", `${process.platform}-${process.arch}`, filename);
  return existsSync(bundled) ? bundled : filename;
}

function timeout(value: number, name: string): number {
  if (!Number.isFinite(value) || value <= 0 || value > MAX_TIMEOUT) {
    throw new RangeError(`${name} must be finite and in (0, ${MAX_TIMEOUT}] milliseconds`);
  }
  return value;
}

function object(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function aborted(): AbortError {
  return new AbortError("cancelled", "request aborted");
}

interface Pending {
  resolve(value: unknown): void;
  reject(error: unknown): void;
  timer: ReturnType<typeof setTimeout>;
  signal?: AbortSignal;
  onAbort?: () => void;
}

interface Queued {
  data: Buffer;
  id?: string;
}

// Strongly retain transports while their processes exist, never their clients.
// A collected client can therefore close its child through the finalizer.
const transports = new Set<Transport>();
const finalizers = new FinalizationRegistry<Transport>((transport) => {
  transport.releaseOwner();
});

process.on("beforeExit", () => {
  for (const transport of transports) void transport.close();
});
process.on("exit", () => {
  for (const transport of transports) transport.kill();
});

class Transport {
  private readonly child: ChildProcess;
  private readonly pending = new Map<string, Pending>();
  private readonly outbox: Queued[] = [];
  private readonly maxQueued: number;
  private readonly maxQueuedBytes: number;
  private queuedBytes = 0;
  private sequence = 0n;
  private buffer: Buffer = Buffer.alloc(0);
  private readonly decoder = new TextDecoder("utf-8", { fatal: true });
  private blocked = false;
  private starting = true;
  private orphaned = false;
  private closing = false;
  private failure: unknown;
  private closePromise?: Promise<void>;
  private readonly exited: Promise<void>;

  constructor(command: readonly string[], private readonly maxPending: number) {
    this.maxQueued = maxPending * 2 + 4;
    this.maxQueuedBytes = MAX_FRAME * Math.max(2, Math.min(maxPending, 16));
    this.child = spawn(command[0]!, [...command.slice(1), "serve"], {
      stdio: ["pipe", "pipe", "inherit"], shell: false, windowsHide: true,
    });
    this.exited = new Promise<void>((resolve) => {
      this.child.once("close", () => {
        transports.delete(this);
        this.fail(new DaemonError("transport", "daemon exited; in-flight outcomes may be unknown"));
        resolve();
      });
    });
    transports.add(this);
    this.child.on("error", (error) => this.fail(new DaemonError("transport", message(error))));
    this.child.stdin!.on("error", (error) => this.fail(new DaemonError("transport", message(error))));
    this.child.stdin!.on("drain", () => {
      this.blocked = false;
      this.flush();
    });
    this.child.stdout!.on("data", (chunk: Buffer) => this.read(chunk));
    this.child.stdout!.on("error", (error) => this.fail(new DaemonError("transport", message(error))));
    this.child.stdout!.on("end", () => this.fail(new DaemonError("transport", "daemon exited; in-flight outcomes may be unknown")));
  }

  async initialize(expectedSchema: string | undefined, init: unknown, deadline: number): Promise<void> {
    const request = (method: string, params: unknown) => {
      const remaining = deadline - performance.now();
      if (remaining <= 0) throw new RequestTimeout("deadline_exceeded", "daemon startup deadline exceeded");
      return this.submit(method, params, remaining);
    };
    try {
      const hello = await request("$hello", { compact: true });
      if (!object(hello) || hello.protocol !== 1) {
        throw new DaemonError("protocol", "unsupported or invalid daemon protocol version");
      }
      if (expectedSchema !== undefined && hello.schema_hash !== expectedSchema) {
        throw new DaemonError("schema_mismatch", "bindings and daemon differ; regenerate bindings or install the matching binary");
      }
      // Ordinary objects inherit .constructor; only the wire's own field counts.
      if (Object.hasOwn(hello, "constructor")) {
        if (!object(hello.constructor)) throw new DaemonError("protocol", "invalid constructor schema");
        await request("$init", init === undefined ? {} : init);
      } else if (init !== undefined) {
        throw new DaemonError("schema_mismatch", "initialization options supplied for a daemon without a constructor");
      }
      this.starting = false;
      this.references();
    } catch (error) {
      await this.close();
      throw error;
    }
  }

  submit(method: string, params: unknown, timeoutMs: number, signal?: AbortSignal): Promise<unknown> {
    if (signal?.aborted) return Promise.reject(aborted());
    if (this.failure !== undefined) return Promise.reject(this.failure);
    if (this.pending.size >= this.maxPending) {
      return Promise.reject(new BusyError("busy", "client pending-request limit reached"));
    }
    const id = String(++this.sequence);
    let frame: Buffer;
    try {
      frame = Buffer.from(stringifyWire({ id, method, params, timeout_ms: Math.max(1, Math.ceil(timeoutMs)) }) + "\n");
      if (frame.length > MAX_FRAME) throw new RangeError("request exceeds frame limit");
    } catch (error) {
      return Promise.reject(error);
    }
    return new Promise<unknown>((resolve, reject) => {
      const pending: Pending = {
        resolve, reject,
        timer: setTimeout(() => this.cancel(id, new RequestTimeout("deadline_exceeded", "request deadline exceeded")), Math.max(1, Math.ceil(timeoutMs))),
      };
      if (signal !== undefined) {
        pending.signal = signal;
        pending.onAbort = () => this.cancel(id, aborted());
        signal.addEventListener("abort", pending.onAbort, { once: true });
      }
      this.pending.set(id, pending);
      this.references();
      if (!this.enqueue({ data: frame, id })) {
        this.pending.delete(id);
        this.cleanup(pending);
        reject(new BusyError("busy", "client outbound queue is full"));
        this.references();
      }
    });
  }

  private cleanup(pending: Pending): void {
    clearTimeout(pending.timer);
    if (pending.signal !== undefined && pending.onAbort !== undefined) {
      pending.signal.removeEventListener("abort", pending.onAbort);
    }
  }

  private cancel(id: string, error: BridgeError): void {
    const pending = this.pending.get(id);
    if (pending === undefined) return;
    this.pending.delete(id);
    this.cleanup(pending);
    pending.reject(error);
    // A request still in our queue has not reached the daemon at all.
    const queued = this.outbox.findIndex((entry) => entry.id === id);
    if (queued !== -1) {
      this.queuedBytes -= this.outbox[queued]!.data.length;
      this.outbox.splice(queued, 1);
    } else if (this.failure === undefined) {
      const data = Buffer.from(stringifyWire({ method: "$cancel", params: { id } }) + "\n");
      if (!this.enqueue({ data })) {
        this.fail(new DaemonError("transport", "outbound queue cannot deliver cancellation"));
      }
    }
    this.references();
  }

  private enqueue(entry: Queued): boolean {
    if (this.outbox.length >= this.maxQueued || this.queuedBytes + entry.data.length > this.maxQueuedBytes) return false;
    this.outbox.push(entry);
    this.queuedBytes += entry.data.length;
    this.flush();
    return true;
  }

  private flush(): void {
    try {
      while (this.outbox.length > 0 && !this.blocked && this.failure === undefined) {
        const entry = this.outbox.shift()!;
        this.queuedBytes -= entry.data.length;
        this.blocked = !this.child.stdin!.write(entry.data);
      }
    } catch (error) {
      this.fail(new DaemonError("transport", message(error)));
    }
  }

  private read(chunk: Buffer): void {
    if (this.failure !== undefined) return;
    try {
      this.buffer = this.buffer.length === 0 ? chunk : Buffer.concat([this.buffer, chunk]);
      let start = 0;
      let end: number;
      while ((end = this.buffer.indexOf(10, start)) !== -1) {
        if (end - start > MAX_FRAME) throw new Error("response exceeds frame limit");
        const response = parseWire(this.decoder.decode(this.buffer.subarray(start, end)));
        start = end + 1;
        if (!object(response) || typeof response.id !== "string" || response.id.length === 0 || response.id.length > 128) {
          throw new Error("invalid daemon response");
        }
        const hasError = Object.hasOwn(response, "error");
        if (hasError === Object.hasOwn(response, "result")) throw new Error("response needs exactly one of result or error");
        // A bad error envelope must not strand its own pending promise.
        const error = hasError ? errorFromWire(response.error) : undefined;
        const pending = this.pending.get(response.id);
        if (pending !== undefined) {
          this.pending.delete(response.id);
          this.cleanup(pending);
          if (error !== undefined) pending.reject(error);
          else pending.resolve(response.result);
        }
      }
      this.buffer = this.buffer.subarray(start);
      if (this.buffer.length > MAX_FRAME) throw new Error("response exceeds frame limit");
      this.references();
    } catch (error) {
      this.fail(new DaemonError("transport", message(error)));
    }
  }

  private fail(error: unknown): void {
    if (this.failure !== undefined) return;
    this.failure = error;
    for (const pending of this.pending.values()) {
      this.cleanup(pending);
      pending.reject(error);
    }
    this.pending.clear();
    this.outbox.length = 0;
    this.queuedBytes = 0;
    this.buffer = Buffer.alloc(0);
    if (!this.closing) void this.close();
  }

  private references(): void {
    if (this.orphaned && !this.starting && !this.closing && this.pending.size === 0) {
      void this.close();
      return;
    }
    const referenced = this.starting || this.closing || this.pending.size > 0;
    if (referenced) this.child.ref();
    else this.child.unref();
    for (const stream of [this.child.stdin, this.child.stdout]) {
      const pipe = stream as unknown as { ref?(): void; unref?(): void };
      if (referenced) pipe.ref?.();
      else pipe.unref?.();
    }
  }

  releaseOwner(): void {
    // An async method can outlive its Client's last external reference. Its
    // pending promise still owns the operation; reap after that work settles.
    this.orphaned = true;
    this.references();
  }

  kill(): void {
    try { this.child.kill("SIGKILL"); } catch { /* best effort during process exit */ }
  }

  close(): Promise<void> {
    if (this.closePromise !== undefined) return this.closePromise;
    this.closing = true;
    this.references();
    this.fail(new ClosedError("closed", "client has been closed"));
    this.closePromise = (async () => {
      try { this.child.kill("SIGTERM"); } catch { /* spawn failure may have no PID */ }
      const force = setTimeout(() => {
        this.kill();
        this.child.stdin?.destroy();
        this.child.stdout?.destroy();
      }, 2000);
      try {
        await this.exited;
      } finally {
        clearTimeout(force);
        this.child.stdin?.destroy();
        this.child.stdout?.destroy();
        transports.delete(this);
      }
    })();
    return this.closePromise;
  }
}

/** Lazy, process-owned client. No calls or failed constructors are replayed. */
export class Client implements AsyncDisposable {
  readonly #command: readonly string[];
  readonly #timeoutMs: number;
  readonly #startupTimeoutMs: number;
  readonly #maxPending: number;
  readonly #expectedSchema: string | undefined;
  readonly #init: unknown;
  #transport?: Transport;
  #startup?: Promise<Transport>;
  #startupFailure: unknown;
  #closed = false;

  constructor(command: string | readonly string[], options: ClientOptions = {}) {
    const argv = typeof command === "string" ? [command] : [...command];
    if (argv.length === 0 || argv[0] === "" || argv.some((arg) => typeof arg !== "string")) {
      throw new TypeError("command must be an executable or a nonempty string array");
    }
    this.#command = Object.freeze(argv);
    this.#timeoutMs = timeout(options.timeoutMs ?? 30_000, "timeoutMs");
    this.#startupTimeoutMs = timeout(options.startupTimeoutMs ?? 5000, "startupTimeoutMs");
    this.#maxPending = options.maxPending ?? 128;
    if (!Number.isSafeInteger(this.#maxPending) || this.#maxPending < 1) throw new RangeError("maxPending must be a positive safe integer");
    this.#expectedSchema = options.expectedSchema;
    this.#init = options.init === undefined ? undefined : parseWire(stringifyWire(options.init));
  }

  #ensure(): Promise<Transport> {
    if (this.#closed) return Promise.reject(new ClosedError("closed", "client has been closed"));
    if (this.#startupFailure !== undefined) return Promise.reject(this.#startupFailure);
    if (this.#startup !== undefined) return this.#startup;
    const deadline = performance.now() + this.#startupTimeoutMs;
    try {
      const transport = new Transport(this.#command, this.#maxPending);
      this.#transport = transport;
      finalizers.register(this, transport, this);
      this.#startup = transport.initialize(this.#expectedSchema, this.#init, deadline).then(
        () => transport,
        (error: unknown) => {
          this.#startupFailure = error;
          throw error;
        },
      );
      return this.#startup;
    } catch (error) {
      this.#startupFailure = error;
      return Promise.reject(error);
    }
  }

  async start(): Promise<this> {
    await this.#ensure();
    return this;
  }

  async call(method: string, params: unknown = {}, options: CallOptions = {}): Promise<unknown> {
    if (options.signal?.aborted) throw aborted();
    const timeoutMs = timeout(options.timeoutMs ?? this.#timeoutMs, "timeoutMs");
    let transport: Transport;
    const startup = this.#ensure();
    if (options.signal === undefined) {
      transport = await startup;
    } else {
      const signal = options.signal;
      transport = await new Promise<Transport>((resolve, reject) => {
        const onAbort = () => reject(aborted());
        signal.addEventListener("abort", onAbort, { once: true });
        startup.then(resolve, reject).finally(() => signal.removeEventListener("abort", onAbort));
      });
    }
    return transport.submit(method, params, timeoutMs, options.signal);
  }

  async batch<const C extends readonly (Call<unknown> | {method:string; params?:unknown})[]>(calls: C, options: CallOptions = {}): Promise<{readonly [K in keyof C]: BatchResult<CallValue<C[K]>>}> {
    if(calls.length>128) throw new RangeError("batch limit is 128 calls");
    const descriptors=[...calls];
    const results = await this.call("$batch", {calls:descriptors.map(call=>call instanceof Call?call.wire():call)}, options) as {result: unknown; error?: unknown}[];
    if(!Array.isArray(results)||results.length!==descriptors.length)throw new DaemonError("protocol","batch result count mismatch");
    return results.map((result,index) => {
      if(result.error!==undefined)return {result:null,error:errorFromWire(result.error)};
      const call=descriptors[index];return {result:call instanceof Call?call.decode(result.result):result.result};
    }) as {readonly [K in keyof C]: BatchResult<CallValue<C[K]>>};
  }

  async *stream(method: string, params: unknown = {}, options: CallOptions = {}): AsyncGenerator<unknown> {
    const {cursor} = await this.call("$stream_open", {method, params}, options) as {cursor: string};
    try {
      while (true) {
        const result = await this.call("$stream_next", {cursor}, options) as {done: boolean; item: unknown};
        if (result.done) return;
        yield result.item;
      }
    } finally {
      try { await this.call("$stream_close", {cursor}, {timeoutMs: options.timeoutMs ?? 1000}); } catch { /* Session close also releases streams. */ }
    }
  }

  async close(): Promise<void> {
    this.#closed = true;
    finalizers.unregister(this);
    await this.#transport?.close();
  }

  async [Symbol.asyncDispose](): Promise<void> {
    await this.close();
  }
}
