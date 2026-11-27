// Runs the shot simulation (compute.wasm) on the packets that the manager sends over a
// MessagePort. It reads only the collision resource of each target, from the Blob that comes
// with the target. It posts a ComputeFatal to the page when it fails.
import type { ComputeFatal, FromCompute, ToCompute } from "./types";

declare const self: DedicatedWorkerGlobalScope;

const message = (err: unknown) => (err instanceof Error ? err.message : String(err));
let failed = false;
let shot: { gen: number; target: string } | undefined; // the target in compute.simulate
const fatal = (error: string) => {
  if (failed) return;
  failed = true;
  self.postMessage({ op: "fatal", error, ...shot } satisfies ComputeFatal);
};

const ready = import(new URL("wasm_exec.js", self.location.href).href).then(async () => {
  const go = new Go();
  const { instance } = await WebAssembly.instantiateStreaming(fetch("compute.wasm"), go.importObject);
  void go.run(instance).then(() => fatal("the Go program stopped"));
});
ready.catch((err) => fatal(message(err)));

let port: MessagePort;
let gen = 0; // the newest queue that the manager told of
// raw: the bytes of a target's collision resource, or the error of the read. The read starts on arrival.
const inbox: { p: ToCompute; raw?: Promise<Uint8Array | string> }[] = [];
let busy = false;

self.onmessage = ({ data }: MessageEvent<{ port: MessagePort }>) => {
  port = data.port;
  port.onmessage = ({ data: p }: MessageEvent<ToCompute>) => {
    // The queue number counts on arrival, so that the targets of an older queue that wait in the inbox are skipped.
    if (p.op === "gen" || p.op === "shooter") gen = Math.max(gen, p.gen);
    if (p.op === "target") inbox.push({ p, raw: p.blob.arrayBuffer().then((b) => new Uint8Array(b), message) });
    else if (p.op !== "gen") inbox.push({ p });
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
      const { p, raw } = inbox.shift()!;
      if (p.op === "tables") {
        const err = compute.tables(p.buf);
        if (err) fatal(`tables: ${err}`);
      } else if (p.op === "shooter") {
        if (p.gen !== gen) continue;
        const err = compute.shooter(p.buf, p.round, p.sample, p.options);
        if (err) fatal(`shooter: ${err}`);
      } else if (p.gen !== gen) {
        reply({ gen: p.gen, target: p.target });
      } else {
        const collision = await raw!;
        if (p.gen !== gen) {
          reply({ gen: p.gen, target: p.target });
          continue;
        }
        if (typeof collision === "string") {
          reply({ gen: p.gen, target: p.target, result: JSON.stringify({ target: p.target, error: `cannot read the collision model: ${collision}` }) });
          continue;
        }
        shot = { gen: p.gen, target: p.target };
        const result = compute.simulate(p.buf, collision);
        // undefined when the Go program exits in the call, for example out of memory or on a stack overflow
        if (typeof result !== "string") throw new Error(`the Go program stopped in the shot at ${p.target}`);
        shot = undefined;
        reply({ gen: p.gen, target: p.target, result });
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
