import {test} from 'node:test';
import assert from 'node:assert/strict';
import {fileURLToPath} from 'node:url';
import {TypesPlus,Mode,Level} from '../.generated/dist/typesplus.js';
const binary=fileURLToPath(new URL('../../bin/typesplus'+(process.platform==='win32'?'.exe':''),import.meta.url));
const payload=()=>({id:(1n<<64n)-1n,pair:[0,65535],mode:Mode.Fast,level:Level.Huge,key:'id-test',region:null});
test('uint64 arrays enums adapters and typed batch descriptors',async()=>{
 await using client=new TypesPlus({_runtime:{command:binary}});
 const value=payload();
 assert.deepEqual(await client.roundTrip({value}),value);
 const first=client.calls.roundTrip({value});const second=client.calls.signed({value:-(1n<<63n)});
 value.pair[0]=12;
 const [a,b]=await client.batch([first,second]);
 assert.equal(a.result.pair[0],0);assert.equal(a.result.mode,Mode.Fast);assert.equal(b.result,-(1n<<63n));
 assert.deepEqual(await client.empty({value:[]}),[]);
 for(const change of [{id:1n<<64n},{id:-1n},{pair:[1]},{pair:[1,2,3]},{pair:[1,65536]},{mode:'invalid'},{level:2n},{key:'bad'},{label:null},{region:undefined}]){
  await assert.rejects(client.roundTrip({value:{...payload(),...change}}));
 }
});

test('compiled array codecs snapshot length', async()=>{
 const {compileCodec}=await import('../.generated/node_modules/gobridge-runtime/dist/index.js');
 const type={kind:'array',length:2,elem:{kind:'uint16'}};
 const codec=compileCodec(type);type.length=3;type.kind='slice';
 assert.deepEqual(codec.encode([1,2]),[1,2]);
 assert.throws(()=>codec.encode([1,2,3]));assert.throws(()=>codec.encode(null));
});
