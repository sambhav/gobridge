// Compiled, never executed: the generated public API is the type contract.
import { Greeter, greet, type Stats } from "./greeter.js";
import { WireTypes } from "./wiretypes.js";
import { Store } from "./metadata.js";

async function examples(): Promise<void> {
  await using greeter = new Greeter({prefix: "Hello, "});
  const greeting: string = await greet({name: "World"});
  const stats: Stats = await greeter.stats();
  const count: bigint = stats.calls;
  const pid: number = stats.processId;
  await greeter.welcome({name: greeting}, {signal: AbortSignal.timeout(1000)});
  // @ts-expect-error operations require their named inputs
  await greet({});
  // @ts-expect-error wire names are camelCase in TypeScript
  const invalid: number = stats.process_id;
  // @ts-expect-error output interfaces are readonly
  stats.calls = 0n;
  // @ts-expect-error constructor config keeps native Go scalar types
  new Greeter({prefix: 123});
  // @ts-expect-error required constructor fields must be provided
  new Store();
  // @ts-expect-error transport options do not replace constructor config
  new Store({_runtime: {timeoutMs: 1000}});
  await using store = new Store({capacity: 2});
  await store.echo({request: {name: "Sam", big: 9007199254740993n, tags: [], labels: {}, fraction: 1}});
  await using wire = new WireTypes();
  await wire.echo({child: {name: "x"}, items: null, labels: null, big: 2n**63n-1n});
  // @ts-expect-error int64 is bigint, including small values
  await wire.echo({child: {name: "x"}, items: [], labels: {}, big: 42});
  void [count, pid, invalid];
}
void examples;

import {TypesPlus, Mode, Level, type Payload} from './typesplus.js';
async function typedBatchTest(client: TypesPlus) {
 const value: Payload={id:1n,pair:[1,2],mode:Mode.Fast,level:Level.Huge,key:'id-test',region:null};
 const [a,b]=await client.batch([client.calls.roundTrip({value}),client.calls.signed({value:1n})]);
 if(!a.error){const model:Payload=a.result;void model;}
 if(!b.error){const integer:bigint=b.result;void integer;}
 // @ts-expect-error generated batch descriptors validate public parameter names
 client.calls.roundTrip({missing:1});
}
