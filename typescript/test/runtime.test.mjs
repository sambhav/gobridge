import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { promisify } from "node:util";
import test from "node:test";
import {
  AbortError, BusyError, Client, ClosedError, DaemonError, DefaultControl,
  InvalidArgumentError, RequestTimeout,
} from "../dist/index.js";

const exec = promisify(execFile);
const runtimeUrl = new URL("../dist/index.js", import.meta.url).href;

function peer({ handshake = "{protocol: 1}", helloDelay = 0, startFile, callsFile, body } = {}) {
  const source = `
    const fs = require('node:fs');
    const readline = require('node:readline');
    ${startFile ? `fs.appendFileSync(${JSON.stringify(startFile)}, String(process.pid)+'\\n');` : ""}
    const waiting = new Map();
    let config;
    const reply = (request, result) => process.stdout.write(JSON.stringify({id: request.id, result})+'\\n');
    const rl = readline.createInterface({input: process.stdin});
    rl.on('line', (line) => {
      const request = JSON.parse(line);
      ${callsFile ? `fs.appendFileSync(${JSON.stringify(callsFile)}, JSON.stringify(request)+'\\n');` : ""}
      if (request.method === '$hello') {
        setTimeout(() => reply(request, ${handshake}), ${helloDelay});
      } else if (request.method === '$init') {
        config = request.params;
        reply(request, null);
      } else if (request.method === '$cancel') {
        clearTimeout(waiting.get(request.params.id));
        waiting.delete(request.params.id);
      } else if (request.method === 'wait') {
        waiting.set(request.id, setTimeout(() => {
          waiting.delete(request.id);
          reply(request, {pid: process.pid, value: request.params.value});
        }, request.params.ms));
      } else {
        ${body ?? "reply(request, {pid: process.pid, params: request.params, config});"}
      }
    });
  `;
  return [process.execPath, "-e", source];
}

function temporary(t) {
  const dir = mkdtempSync(join(tmpdir(), "gobridge-node-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}

async function until(predicate, milliseconds = 3000) {
  const deadline = performance.now() + milliseconds;
  while (!predicate()) {
    assert.ok(performance.now() < deadline, "timed out waiting for peer state");
    await delay(5);
  }
}

test("lazy clients do not spawn for an already-aborted call", async (t) => {
  const file = join(temporary(t), "started");
  const client = new Client(peer({ startFile: file }));
  t.after(() => client.close());
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(client.call("ping", {}, { signal: controller.signal }), AbortError);
  assert.equal(existsSync(file), false);
  const result = await client.call("ping", { text: "hello 🌍" });
  assert.deepEqual(result.params, { text: "hello 🌍" });
  assert.equal(readFileSync(file, "utf8").trim(), String(result.pid));
  await client.close();
  await assert.rejects(client.call("ping"), ClosedError);
});

test("concurrent cold calls share one daemon and correlate out-of-order responses", async (t) => {
  const client = new Client(peer());
  t.after(() => client.close());
  const results = await Promise.all(Array.from({ length: 24 }, (_, value) => client.call("wait", { value, ms: 50 - value })));
  assert.equal(new Set(results.map((result) => result.pid)).size, 1);
  assert.deepEqual(results.map((result) => result.value), Array.from({ length: 24 }, (_, value) => value));
});

test("cold cancellation skips its operation and leaves startup owned", async (t) => {
  const dir = temporary(t);
  const startFile = join(dir, "start");
  const callsFile = join(dir, "calls");
  const client = new Client(peer({ helloDelay: 150, startFile, callsFile }));
  t.after(() => client.close());
  const controller = new AbortController();
  const pending = client.call("must-not-run", {}, { signal: controller.signal });
  const rejected = assert.rejects(pending, AbortError);
  await until(() => existsSync(startFile));
  controller.abort();
  await rejected;
  const result = await client.call("ping");
  assert.equal(readFileSync(startFile, "utf8").trim(), String(result.pid));
  assert.deepEqual(readFileSync(callsFile, "utf8").trim().split("\n").map((line) => JSON.parse(line).method), ["$hello", "ping"]);
});

test("request cancellation and timeout do not cancel unrelated calls", async (t) => {
  const client = new Client(peer());
  t.after(() => client.close());
  await client.start();
  const controller = new AbortController();
  const pending = client.call("wait", { ms: 10000, value: "cancel" }, { signal: controller.signal });
  const rejected = assert.rejects(pending, AbortError);
  const survivor = client.call("wait", { ms: 30, value: "keep" });
  controller.abort();
  await rejected;
  assert.equal((await survivor).value, "keep");
  await assert.rejects(client.call("wait", { ms: 10000 }, { timeoutMs: 10 }), RequestTimeout);
  assert.ok((await client.call("ping")).pid > 0);
});

for (const [name, body] of [
  ["missing error fields", "process.stdout.write(JSON.stringify({id:request.id,error:{}})+'\\n');"],
  ["null error", "process.stdout.write(JSON.stringify({id:request.id,error:null})+'\\n');"],
  ["both outcomes", "process.stdout.write(JSON.stringify({id:request.id,result:null,error:{code:'bad',message:'bad'}})+'\\n');"],
  ["invalid UTF8", "process.stdout.write(Buffer.from([0xff, 0x0a]));"],
  ["unfinished oversized frame", "process.stdout.write('x'.repeat(1024*1024+1));"],
]) {
  test(`malformed response fails its own waiter immediately: ${name}`, async (t) => {
    const client = new Client(peer({ body }));
    t.after(() => client.close());
    await assert.rejects(client.call("ping", {}, { timeoutMs: 500 }), DaemonError);
    await assert.rejects(client.call("ping"), DaemonError);
  });
}

for (const handshake of ["null", "[]", "7", "{protocol:99}"]) {
  test(`malformed hello is typed and sticky: ${handshake}`, async (t) => {
    const file = join(temporary(t), "starts");
    const client = new Client(peer({ handshake, startFile: file }));
    t.after(() => client.close());
    await assert.rejects(client.start(), DaemonError);
    await assert.rejects(client.start(), DaemonError);
    assert.equal(readFileSync(file, "utf8").trim().split("\n").length, 1);
  });
}

test("constructor data is snapshotted and initialized exactly once", async (t) => {
  const callsFile = join(temporary(t), "calls");
  const config = { nested: { name: "original" } };
  const client = new Client(peer({ handshake: "{protocol:1,constructor:{}}", callsFile }), { init: config });
  t.after(() => client.close());
  config.nested.name = "changed";
  const results = await Promise.all(Array.from({ length: 8 }, () => client.call("ping")));
  assert.deepEqual(results[0].config, { nested: { name: "original" } });
  const requests = readFileSync(callsFile, "utf8").trim().split("\n").map(JSON.parse);
  assert.equal(requests.filter((request) => request.method === "$init").length, 1);
});

test("constructor failure is never retried", async (t) => {
  const file = join(temporary(t), "started");
  const command = peer({ handshake: "{protocol:1,constructor:{}}", startFile: file });
  command[2] = command[2].replace("config = request.params;", "process.stdout.write(JSON.stringify({id:request.id,error:{code:'invalid_argument',message:'constructor failed'}})+'\\n'); return;");
  const client = new Client(command);
  t.after(() => client.close());
  await assert.rejects(client.start(), InvalidArgumentError);
  await assert.rejects(client.call("ping"), InvalidArgumentError);
  assert.equal(readFileSync(file, "utf8").trim().split("\n").length, 1);
});

test("closing during startup stops and reaps the peer", async (t) => {
  const file = join(temporary(t), "started");
  const client = new Client(peer({ helloDelay: 10000, startFile: file }));
  const pending = assert.rejects(client.start(), ClosedError);
  await until(() => existsSync(file));
  await client.close();
  await pending;
  const pid = Number(readFileSync(file, "utf8").trim());
  assert.throws(() => process.kill(pid, 0));
});

test("backpressure and repeated aborts stay bounded behind a stalled reader", async (t) => {
  const command = peer();
  command[2] = command[2].replace("setTimeout(() => reply(request, {protocol: 1}), 0);", "reply(request,{protocol:1}); rl.close(); process.stdin.pause(); setInterval(()=>{},1000);");
  const client = new Client(command, { maxPending: 1 });
  t.after(() => client.close());
  await client.start();
  const controller = new AbortController();
  const waiting = client.call("blocked", { text: "x".repeat(500000) }, { signal: controller.signal });
  const rejected = assert.rejects(waiting, AbortError);
  await Promise.resolve();
  await assert.rejects(client.call("another"), BusyError);
  controller.abort();
  await rejected;
  for (let index = 0; index < 40; index++) {
    const abort = new AbortController();
    const call = client.call("blocked", { text: "x".repeat(500000) }, { signal: abort.signal });
    const rejection = assert.rejects(call, AbortError);
    await Promise.resolve();
    abort.abort();
    await rejection;
  }
  await client.close();
});

test("default configuration and overlapping/nested scopes retain isolation", async (t) => {
  const command = peer({ handshake: "{protocol:1,constructor:{}}" });
  const control = new DefaultControl((options) => new Client(command, { init: options }));
  t.after(() => control.close());
  const config = { prefix: "default" };
  control.configure(config);
  config.prefix = "mutated";
  const original = control.client();
  const base = await original.call("ping");
  assert.equal(base.config.prefix, "default");
  assert.throws(() => control.configure({ prefix: "replacement" }), /already exists/);
  const results = await Promise.all(["a", "b"].map((prefix) => control.scope({ prefix }, async (scoped) => {
    await delay(10);
    assert.equal(control.client(), scoped);
    const own = await control.client().call("ping");
    await control.scope({ prefix: "nested" }, async (inner) => {
      assert.equal(control.client(), inner);
      assert.notEqual((await inner.call("ping")).pid, own.pid);
    });
    assert.equal(control.client(), scoped);
    return own;
  })));
  assert.equal(new Set([base.pid, ...results.map((result) => result.pid)]).size, 3);
  assert.deepEqual(results.map((result) => result.config.prefix), ["a", "b"]);
  assert.equal(control.client(), original);
  await control.close();
  await assert.rejects(original.call("ping"), ClosedError);
  assert.notEqual((await control.client().call("ping")).pid, base.pid);
});

test("idle clients do not keep Node alive, and beforeExit reaps their daemon", async () => {
  const source = `import {Client} from ${JSON.stringify(runtimeUrl)};
    const client = new Client(${JSON.stringify(peer())});
    console.log((await client.call('ping')).pid);`;
  const result = await exec(process.execPath, ["--input-type=module", "-e", source], { timeout: 5000 });
  const pid = Number(result.stdout.trim());
  assert.ok(pid > 0);
  assert.throws(() => process.kill(pid, 0));
});

test("collecting a client reaps its daemon while Node remains active", async () => {
  const source = `import {Client} from ${JSON.stringify(runtimeUrl)};
    let client = new Client(${JSON.stringify(peer())});
    const pid = (await client.call('ping')).pid;
    client = null;
    let reaped = false;
    for (let i=0;i<100;i++) {
      global.gc(); await new Promise(resolve=>setTimeout(resolve,20));
      try {process.kill(pid,0)} catch {reaped=true;break;}
    }
    if (!reaped) throw new Error('collected client retained daemon');
    console.log('reaped');`;
  const result = await exec(process.execPath, ["--expose-gc", "--input-type=module", "-e", source], { timeout: 5000 });
  assert.equal(result.stdout.trim(), "reaped");
});

test("collecting a client preserves its pending operation before reaping", async () => {
  const source = `import {Client} from ${JSON.stringify(runtimeUrl)};
    let client = new Client(${JSON.stringify(peer())});
    await client.start();
    const pending = client.call('wait', {ms:400,value:'completed'});
    client = null;
    const collect = setInterval(()=>global.gc(),10);
    const result = await pending;
    clearInterval(collect);
    if (result.value !== 'completed') throw new Error('lost result');
    let reaped = false;
    for (let i=0;i<100;i++) {
      global.gc(); await new Promise(resolve=>setTimeout(resolve,20));
      try {process.kill(result.pid,0)} catch {reaped=true;break;}
    }
    if (!reaped) throw new Error('orphaned transport retained daemon');
    console.log('completed then reaped');`;
  const result = await exec(process.execPath, ["--expose-gc", "--input-type=module", "-e", source], { timeout: 5000 });
  assert.equal(result.stdout.trim(), "completed then reaped");
});
