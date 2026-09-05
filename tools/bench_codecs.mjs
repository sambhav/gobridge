// Isolate schema traversal from JSON/stdio costs. No timing assertions.
import {performance} from 'node:perf_hooks';
import assert from 'node:assert/strict';
import {encode, decode, compileCodec} from '../typescript/dist/codec.js';
import {stringifyWire} from '../typescript/dist/codec.js';
import {pathToFileURL} from 'node:url';

const type = {kind:'struct', fields:[
  {name:'node_list',type:{kind:'slice',elem:{kind:'struct',fields:[
    {name:'node_name',type:{kind:'string'}},
    {name:'values',type:{kind:'slice',elem:{kind:'int64'}}},
  ]}}},
  {name:'data',type:{kind:'bytes'}},
]};
const value = {nodeList:Array.from({length:16},()=>({nodeName:'entry',values:[1n,2n,3n,4n]})),data:new Uint8Array(1024)};
const codec = compileCodec(type);
const generic = () => decode(type, encode(type, value));
const compiled = () => codec.decode(codec.encode(value));
assert.deepEqual(compiled(), generic());
const calls = 10000, results = [];
for (let repeat=0;repeat<5;repeat++) {
  for (const [name,call] of repeat%2 ? [['compiled',compiled],['generic',generic]] : [['generic',generic],['compiled',compiled]]) {
    for(let i=0;i<1000;i++) call();
    const start = performance.now();
    for(let i=0;i<calls;i++) call();
    results.push({repeat,name,us_per_roundtrip:(performance.now()-start)*1000/calls});
  }
}
const report = {node:process.version,calls,results};
// Optional complete baseline checkout, with its runtime already compiled.
if (process.argv[2]) {
  const baseline = await import(pathToFileURL(process.argv[2]).href);
  const wire = codec.encode(value);
  assert.equal(stringifyWire(wire),baseline.stringifyWire(wire));
  const wireResults = [];
  for (let repeat=0;repeat<5;repeat++) {
    const variants = [['before',baseline.stringifyWire],['after',stringifyWire]];
    if (repeat%2) variants.reverse();
    for (const [name,call] of variants) {
      for(let i=0;i<1000;i++) call(wire);
      const start = performance.now();
      for(let i=0;i<calls;i++) call(wire);
      wireResults.push({repeat,name,us_per_stringify:(performance.now()-start)*1000/calls});
    }
  }
  report.wire_results = wireResults;
}
console.log(JSON.stringify(report,null,2));
