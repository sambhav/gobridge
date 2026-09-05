import { parentPort, workerData } from "node:worker_threads";
const { configure, session, shutdown, welcome, stats } = await import(workerData.moduleURL);
configure({prefix: "Worker: ", _runtime: {command: workerData.command}});
try {
  await welcome({name: "Sam"});
  parentPort.postMessage(await stats());
} finally {
  await shutdown();
}
