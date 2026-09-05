import { test } from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import { Worker } from "node:worker_threads";
import { spawnSync } from "node:child_process";
import { Greeter, control, greet, welcome, stats } from "../.generated/dist/annotated.js";
import { Hello } from "../.generated/dist/hello.js";
import { TextKit } from "../.generated/dist/textkit.js";
import { WireTypes } from "../.generated/dist/wiretypes.js";
import { Store, schema as storeSchema } from "../.generated/dist/metadata.js";
import { AbortError, BridgeError, InvalidArgumentError, RequestTimeout } from "../.generated/node_modules/gobridge-runtime/dist/index.js";

const root = fileURLToPath(new URL("../../", import.meta.url));
const binary = name => join(root, "bin", name + (process.platform === "win32" ? ".exe" : ""));
const runtime = name => ({command: binary(name)});

test("generated functions, constructor options, camelCase and void work with Go", async () => {
  const first = new Greeter({prefix: "Hi, ", _runtime: runtime("annotated")});
  const second = new Greeter({_runtime: runtime("annotated")});
  try {
    assert.equal(await first.greet({name: "World"}), "Hello, World!");
    assert.equal(await first.welcome({name: "Sam"}), "Hi, Sam");
    const before = await first.stats();
    assert.equal(before.calls, 1n);
    assert.equal(typeof before.processId, "number");
    assert.notEqual(before.processId, (await second.stats()).processId);
    assert.equal((await second.stats()).calls, 0n);
    await Promise.all(Array.from({length: 32}, (_, i) => first.welcome({name: String(i)})));
    assert.equal((await first.stats()).calls, 33n);
    assert.equal(await first.reset(), undefined);
    assert.equal((await first.stats()).calls, 0n);
  } finally {
    await Promise.all([first.close(), second[Symbol.asyncDispose]()]);
  }
  const hello = new Hello({_runtime: runtime("hello")});
  try {
    assert.deepEqual(await hello.greet({name: "World"}), {message: "Hello, World!"});
  } finally { await hello.close(); }
});

test("module defaults share one daemon and concurrent nested scopes stay isolated", async () => {
  control.configure({prefix: "Default: ", _runtime: runtime("annotated")});
  try {
    assert.equal(await greet({name: "World"}), "Hello, World!");
    assert.equal(await welcome({name: "Sam"}), "Default: Sam");
    const original = await stats();
    const scopedPids = await Promise.all(["A: ", "B: "].map(prefix =>
      control.scope({prefix, _runtime: runtime("annotated")}, async client => {
        assert.equal(await welcome({name: "Sam"}), prefix + "Sam");
        const outer = await client.stats();
        await control.scope({prefix: "Inner: ", _runtime: runtime("annotated")}, async () => {
          assert.equal(await welcome({name: "Sam"}), "Inner: Sam");
          assert.notEqual((await stats()).processId, outer.processId);
        });
        assert.equal((await stats()).processId, outer.processId);
        return outer.processId;
      })
    ));
    assert.equal(new Set([original.processId, ...scopedPids]).size, 3);
    assert.deepEqual(await stats(), original);
    await assert.rejects(control.scope({_runtime: runtime("annotated")}, async () => {
      throw new Error("scope failure");
    }), /scope failure/);
    assert.deepEqual(await stats(), original);
  } finally { await control.close(); }
});

test("full int64 range and nested nullable data round trip exactly", async () => {
  const client = new WireTypes({_runtime: runtime("wiretypes")});
  try {
    for (const big of [-(2n**63n), 2n**63n-1n, 9007199254740993n, 1n]) {
      const payload = {child: {name: "Unicode: \u{1f600}"}, optional: {name: "present"},
        items: [{name: "nested"}], labels: {key: {name: "mapped"}}, big};
      assert.deepEqual(await client.echo(payload), payload);
    }
    const nil = {child: {name: "x"}, items: null, labels: null, big: 0n};
    assert.deepEqual(await client.echo(nil), nil);
    // Go's omitempty drops a nil pointer; generated optional fields stay absent.
    assert.deepEqual(await client.echo({...nil, optional: null}), nil);
    await assert.rejects(client.echo({...nil, big: 42}), InvalidArgumentError);
    await assert.rejects(client.explode(), BridgeError);
    await assert.rejects(client.large(), BridgeError);
    assert.deepEqual(await client.echo(nil), nil);
  } finally { await client.close(); }
});

test("shared Go constraints and exact schema metadata apply to generated clients", async () => {
  const client = new Store({capacity: 2, _runtime: runtime("metadata")});
  const request = {name: "\u{1f600}", big: 9007199254740993n, tags: [], labels: {}, fraction: 1.5};
  try {
    assert.deepEqual(await client.echo({request}), request);
    assert.deepEqual(await client.flattened(request), request);
    assert.deepEqual(await client.lowercase({record: {record: {name: "nested"}}}), {record: {name: "nested"}});
    for (const invalid of [{...request, name: "12345"}, {...request, big: 9007199254740992n}, {...request, age: 121}]) {
      await assert.rejects(client.echo({request: invalid}), InvalidArgumentError);
    }
    const echo = storeSchema.operations.find(op => op.name === "echo");
    assert.equal(echo.input.fields[0].type.fields.find(field => field.name === "big").constraints.minimum, 9007199254740993n);
  } finally { await client.close(); }
  const invalid = new Store({capacity: 0, _runtime: runtime("metadata")});
  try { await assert.rejects(invalid.start(), InvalidArgumentError); }
  finally { await invalid.close(); }
});

test("canceling a Go call preserves concurrent work and the daemon", async () => {
  const client = new TextKit({_runtime: runtime("textkit")});
  try {
    await client.start();
    const controller = new AbortController();
    const canceled = assert.rejects(client.wait({milliseconds: 5000}, {signal: controller.signal}), AbortError);
    const other = client.analyze({text: "one two"});
    for (let i = 0; i < 100; i++) {
      if ((await client.health()).active > 0n) break;
      await new Promise(resolve => setTimeout(resolve, 5));
    }
    controller.abort();
    await canceled;
    assert.equal((await other).words, 2);
    await assert.rejects(client.wait({milliseconds: 5000}, {timeoutMs: 20}), RequestTimeout);
    assert.equal((await client.analyze({text: "still alive"})).words, 2);
  } finally { await client.close(); }
});

test("generated TypeScript can launch only an embedded Cobra daemon", async () => {
  const client = new Greeter({prefix: "Cobra: ", _runtime: {command: [binary("cobra-host"), "bridge"]}});
  try {
    assert.equal(await client.welcome({name: "Sam"}), "Cobra: Sam");
    assert.equal((await client.stats()).calls, 1n);
  } finally { await client.close(); }
});

test("worker threads create separate defaults and Go state", async () => {
  const client = new Greeter({_runtime: runtime("annotated")});
  try {
    await client.welcome({name: "Parent"});
    const parent = await client.stats();
    const result = await new Promise((resolve, reject) => {
      const worker = new Worker(new URL("./worker.mjs", import.meta.url), {workerData: {
        moduleURL: new URL("../.generated/dist/annotated.js", import.meta.url).href,
        command: binary("annotated"),
      }});
      let value;
      worker.once("message", message => { value = message; });
      worker.once("error", reject);
      worker.once("exit", code => code === 0 ? resolve(value) : reject(new Error(`worker exit ${code}`)));
    });
    assert.equal(result.calls, 1n);
    assert.notEqual(result.processId, parent.processId);
    assert.deepEqual(await client.stats(), parent);
  } finally { await client.close(); }
});

test("a short Node script exits normally without explicitly closing its default", () => {
  const moduleURL = new URL("../.generated/dist/annotated.js", import.meta.url).href;
  const result = spawnSync(process.execPath, ["--input-type=module", "-e", `
    import {control, welcome, stats} from ${JSON.stringify(moduleURL)};
    control.configure({_runtime: {command: ${JSON.stringify(binary("annotated"))}}});
    await welcome({name: "Child"});
    console.log(String((await stats()).calls));
  `], {encoding: "utf8", timeout: 15000});
  assert.equal(result.error, undefined);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "1");
});
