// One isolated Node benchmark process; invoked by bench.py.
import { Perf } from '../typescript/.generated/dist/perf.js';
import { fileURLToPath } from 'node:url';
import { performance } from 'node:perf_hooks';

const args = process.argv.slice(2);
const option = name => Number(args[args.indexOf('--' + name) + 1]);
const calls = option('calls'), concurrency = option('concurrency');
const size = option('size'), rounds = option('rounds');
const binary = process.env.GOBRIDGE_BENCH_BINARY ?? fileURLToPath(new URL('../bin/perf' + (process.platform === 'win32' ? '.exe' : ''), import.meta.url));
const config = {_runtime: {command: binary}};
const params = {data: new Uint8Array(size), rounds,
  nodes: args.includes('--nested') ? Array.from({length:16}, () => ({name:'entry', values:[1n,2n,3n,4n]})) : []};
const cold = [], samples = new Array(calls);
for (let i=0;i<10;i++) {
  const client = new Perf(config);
  const start = performance.now();
  try { await client.work({data:new Uint8Array(), rounds:0, nodes:[]}); cold.push(performance.now()-start); }
  finally { await client.close(); }
}
const client = new Perf(config);
let elapsed;
const check = result => {
  if (result.data.length!==size || result.nodes.length!==params.nodes.length || result.digest.length!==64) throw Error('incorrect result');
};
try {
  for(let i=0;i<50;i++) check(await client.work(params));
  const start = performance.now();
  await Promise.all(Array.from({length:concurrency}, async (_,offset) => {
    for(let i=offset;i<calls;i+=concurrency) {
      const start = performance.now();
      const result = await client.work(params);
      samples[i] = (performance.now()-start)*1000;
      check(result);
    }
  }));
  elapsed = (performance.now()-start)/1000;
} finally { await client.close(); }
const ordered = [...samples].sort((a,b)=>a-b);
console.log(JSON.stringify({calls_per_second:calls/elapsed,samples_us:samples,
  p50_us:ordered[Math.floor((calls-1)*.5)],p95_us:ordered[Math.floor((calls-1)*.95)],
  p99_us:ordered[Math.floor((calls-1)*.99)],cold_first_call_ms:cold}));
