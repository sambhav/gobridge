import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import * as api from '../.generated/dist/shared.js';

const command = fileURLToPath(new URL(`../../bin/shared${process.platform === 'win32' ? '.exe' : ''}`, import.meta.url));
const options = {_runtime: {command}};

test('shared objects snapshot configuration, reuse a process, and preserve streams and batches', async () => {
  await api.shutdown();
  api.configure(options);
  const labels = ['original'];
  const first = new api.AuthClient({accountName: 'first', labels});
  const second = new api.AuthClient({accountName: 'second', labels: null});
  labels.push('changed');
  // Resolving module configuration still works: construction never resolves a transport.
  api.configure(options);
  try {
    const objects = Array.from({length: 32}, (_, i) => i % 2 ? second : first);
    const results = await Promise.all(objects.map(obj => obj.identify()));
    const pid = results[0].processId;
    assert.deepEqual([...new Set(results.map(r => r.processId))], [pid]);
    assert.deepEqual(results.map(r => r.account), objects.map((_, i) => i % 2 ? 'second' : 'first'));
    assert.deepEqual(results[0].labels, ['original']);
    assert.ok(results.every(r => r.calls === 1));
    assert.equal(await api.constructions(), 32n);
    assert.equal(await api.processId(), pid);
    const stream = [];
    for await (const value of first.watch({count: 3})) stream.push(value);
    assert.deepEqual(stream.map(r => r.calls), [1, 2, 3]);
    await api.session(options, async transport => {
      const scoped = await transport.processId();
      assert.notEqual(scoped, pid);
      assert.equal((await first.identify()).processId, scoped);
      const batch = await transport.batch([first.calls.identify(), second.calls.identify()]);
      assert.deepEqual(batch.map(r => r.result.account), ['first', 'second']);
      const pinned = new api.AuthClient({accountName: 'pinned', labels: null, _client: transport});
      await api.session(options, async () => {
        assert.notEqual((await first.identify()).processId, scoped);
        assert.equal((await pinned.identify()).processId, scoped);
      });
      await api.shutdown();
      assert.equal((await pinned.identify()).processId, scoped);
    });
    const restarted = (await first.identify()).processId;
    assert.notEqual(restarted, pid);
    const isolated = new api.AuthClientSession(options);
    try {
      const pinned = new api.AuthClient({accountName: 'isolated', labels: null, _client: isolated});
      assert.notEqual((await pinned.identify()).processId, restarted);
      assert.equal((await first.identify()).processId, restarted);
    } finally { await isolated.close(); }
    for (const [accountName, code] of [['bad', 'invalid_argument'], ['panic', 'internal'], ['nil', 'internal']]) {
      await assert.rejects(new api.AuthClient({accountName, labels: null}).identify(), error => error.code === code);
      assert.equal((await first.identify()).processId, restarted);
    }
  } finally { await api.shutdown(); }
});
