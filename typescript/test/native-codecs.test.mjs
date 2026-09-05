import test from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from "node:url";
import { nativeValues, configure } from '../.generated/dist/wiretypes.js';
import { encode, decode } from '../dist/codec.js';

configure({_runtime:{command:fileURLToPath(new URL('../../bin/wiretypes' + (process.platform === 'win32' ? '.exe' : ''), import.meta.url))}});

test('bytes and timestamps/durations preserve exact values', async () => {
  const value = { data: new Uint8Array([0, 1, 255]), at: '2026-09-05T12:34:56.123456789+02:00', delay: 9223372036854775807n };
  assert.deepEqual(await nativeValues(value), value);
  assert.deepEqual(await nativeValues({ ...value, data: null, delay: -9223372036854775808n }), { ...value, data: null, delay: -9223372036854775808n });
  assert.deepEqual(await nativeValues({ ...value, data: new Uint8Array() }), { ...value, data: new Uint8Array() });
  assert.throws(() => encode({kind:'bytes'}, [1,2]), /Uint8Array/);
  assert.throws(() => decode({kind:'bytes'}, '!!!'), /base64/);
  assert.throws(() => encode({kind:'duration'}, 9223372036854775808n), /range/);
  await assert.rejects(nativeValues({...value, at:'2026-99-99T00:00:00Z'}));
});
