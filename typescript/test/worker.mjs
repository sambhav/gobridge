import { parentPort, workerData } from "node:worker_threads";
const { control, welcome, stats } = await import(workerData.moduleURL);
control.configure({prefix: "Worker: ", _runtime: {command: workerData.command}});
try {
  await welcome({name: "Sam"});
  parentPort.postMessage(await stats());
} finally {
  await control.close();
}
