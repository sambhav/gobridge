import { AsyncLocalStorage } from "node:async_hooks";
import { Client } from "./runtime.js";

/** Lifecycle and context-local overrides for generated module functions. */
export class DefaultControl<O extends object, C extends Client> {
  readonly #factory: (options: O) => C;
  readonly #scope = new AsyncLocalStorage<C>();
  #options = {} as O;
  #default?: C;

  constructor(factory: (options: O) => C) {
    this.#factory = factory;
  }

  client(): C {
    const scoped = this.#scope.getStore();
    if (scoped !== undefined) return scoped;
    return this.#default ??= this.#factory(structuredClone(this.#options));
  }

  configure(options: O): void {
    if (this.#default !== undefined) {
      throw new Error("default client already exists; call shutdown() before configuring it");
    }
    this.#options = structuredClone(options);
  }

  async start(): Promise<C> {
    return await this.client().start();
  }

  async close(): Promise<void> {
    const client = this.#default;
    this.#default = undefined;
    await client?.close();
  }

  async scope<R>(options: O, callback: (client: C) => R | Promise<R>): Promise<R> {
    const client = this.#factory(structuredClone(options));
    try {
      await client.start();
      return await this.#scope.run(client, () => callback(client));
    } finally {
      await client.close();
    }
  }
}
