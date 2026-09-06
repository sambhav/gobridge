import {test} from 'node:test';
import assert from 'node:assert/strict';
import {fileURLToPath} from 'node:url';
import {Streaming} from '../.generated/dist/streaming.js';
const binary = fileURLToPath(new URL('../../bin/streaming' + (process.platform === 'win32' ? '.exe' : ''), import.meta.url));
test('typed streams, early return, error details and batches', async () => {
  await using client = new Streaming({_runtime:{command:[binary, 'bridge']}});
  const values=[];
  for await (const item of client.numbers({count:3})) values.push(item);
  assert.deepEqual(values,[0n,1n,2n]);
  for await (const item of client.numbers({count:100000})) { assert.equal(item,0n);break; }
  await assert.rejects(async()=>{for await(const _ of client.numbers({count:1,fail:true})) {}},error=>error.details.field==='fail');
  const result = await client.batch([{method:'missing'},{method:'explode'}]);
  assert.equal(result[0].error.code,'not_found');
  assert.equal(result[1].error.message,'internal operation error');
});
