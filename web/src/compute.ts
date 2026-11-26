// Runs the shot simulation (compute.wasm) on the packets that the manager sends over a
// MessagePort. It reads no files. It posts {op: "fatal", error} to the page when it fails.
import type { FromCompute, ToCompute } from "./types";

declare const self: DedicatedWorkerGlobalScope;

const message = (err: unknown) => (err instanceof Error ? err.message : String(err));
let failed = false;
const fatal = (error: string) => {
  failed = true;
  self.postMessage({ op: "fatal", error });
};

const ready = import(new URL("wasm_exec.js", self.location.href).href).then(async () => {
  const go = new Go();
  const { instance } = await WebAssembly.instantiateStreaming(fetch("compute.wasm"), go.importObject);
  void go.run(instance).then(() => fatal("the Go program stopped"));
});
ready.catch((err) => fatal(message(err)));

let port: MessagePort;
let gen = 0; // the newest queue that the manager told of
const inbox: ToCompute[] = [];
let busy = false;

self.onmessage = ({ data }: MessageEvent<{ port: MessagePort }>) => {
  port = data.port;
  port.onmessage = ({ data: p }: MessageEvent<ToCompute>) => {
    // The queue number counts on arrival, so that the targets of an older queue that wait in the inbox are skipped.
    if (p.op === "gen" || p.op === "shooter") gen = Math.max(gen, p.gen);
    if (p.op !== "gen") inbox.push(p);
    void pump();
  };
};

const reply = (r: FromCompute) => port.postMessage(r);

async function pump() {
  if (busy) return;
  busy = true;
  try {
    await ready;
    while (inbox.length && !failed) {
      const p = inbox.shift()!;
      if (p.op === "tables") {
        const err = compute.tables(p.buf);
        if (err) fatal(`tables: ${err}`);
      } else if (p.op === "shooter") {
        if (p.gen !== gen) continue;
        const err = compute.shooter(p.buf, p.round);
        if (err) fatal(`shooter: ${err}`);
      } else if (p.gen !== gen) {
        reply({ gen: p.gen, target: p.target });
      } else {
        reply({ gen: p.gen, target: p.target, result: compute.simulate(p.buf) });
        // lets the port deliver what waits, so that a new queue number arrives before the next target
        await new Promise((r) => setTimeout(r, 0));
      }
    }
  } catch (err) {
    fatal(message(err));
  } finally {
    busy = false;
  }
}
