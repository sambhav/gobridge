// Isolate schema traversal from JSON/stdio costs. No timing assertions.
import {performance} from 'node:perf_hooks';
import assert from 'node:assert/strict';
import {encode, decode, compileCodec} from '../typescript/dist/codec.js';

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
console.log(JSON.stringify({node:process.version,calls,results},null,2));
