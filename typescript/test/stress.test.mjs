import test from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { Client } from '../dist/index.js';

// Real daemon, bounded repeated startup/abort/close cycles. Every outstanding
// promise must settle; child ownership is also covered by runtime.test.mjs.
test('repeated real-daemon cancellation and close settle every waiter', {timeout:30000}, async () => {
  const binary=fileURLToPath(new URL('../../bin/perf'+(process.platform==='win32'?'.exe':''),import.meta.url));
  for(let iteration=0;iteration<12;iteration++) {
    const client=new Client(binary,{maxPending:8});
    try {
      await client.call('work',{data:'',rounds:0,nodes:[]});
      const controller=new AbortController();
      const tasks=Array.from({length:8},()=>client.call('work',{data:'',rounds:50_000_000,nodes:[]},{signal:controller.signal}));
      const settled=Promise.allSettled(tasks);
      await setTimeout(10);
      if(iteration%2===0) controller.abort();
      else await client.close();
      let timer;
      const deadline=new Promise((_,reject)=>{timer=globalThis.setTimeout(()=>reject(Error('stranded waiter')),5000);});
      try {const results=await Promise.race([settled,deadline]);assert.ok(results.every(r=>r.status==='rejected'));}
      finally {clearTimeout(timer);}
      if(iteration%2===0) {
        await setTimeout(5);
        assert.equal((await client.call('work',{data:'b2s=',rounds:0,nodes:[]})).data,'b2s=');
      }
    } finally {await client.close();}
  }
});
