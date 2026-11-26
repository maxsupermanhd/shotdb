// Runs the Go simulation (shotdb.wasm) off the page's thread.
// shotdb.wasm and wasm_exec.js sit next to the bundled worker in dist/.
import type { Reply, Request, Source, TargetResult } from "./types";

declare const self: DedicatedWorkerGlobalScope;

const ready = import(new URL("wasm_exec.js", self.location.href).href).then(async () => {
  const go = new Go();
  const { instance } = await WebAssembly.instantiateStreaming(fetch("shotdb.wasm"), go.importObject);
  void go.run(instance);
});

let source: Source | null = null;
const dirs = new Map<string, FileSystemDirectoryHandle>(); // directory path -> handle

function missing(err: unknown): boolean {
  return err instanceof DOMException && (err.name === "NotFoundError" || err.name === "TypeMismatchError");
}

async function fileOf(path: string): Promise<File | null> {
  if (!source) return null;
  if (source.kind === "files") return source.files.get(path) ?? null;
  const parts = path.split("/");
  const name = parts.pop()!;
  try {
    const dir = await dirOf(source.handle, parts);
    return await (await dir.getFileHandle(name)).getFile();
  } catch (err) {
    if (missing(err)) return null;
    throw err;
  }
}

async function dirOf(root: FileSystemDirectoryHandle, parts: string[]): Promise<FileSystemDirectoryHandle> {
  let dir = root;
  for (let i = 0; i < parts.length; i++) {
    const key = parts.slice(0, i + 1).join("/");
    let next = dirs.get(key);
    if (!next) {
      next = await dir.getDirectoryHandle(parts[i]);
      dirs.set(key, next);
    }
    dir = next;
  }
  return dir;
}

async function readFile(path: string): Promise<Uint8Array | null> {
  const file = await fileOf(path);
  return file ? new Uint8Array(await file.arrayBuffer()) : null;
}

async function readRange(path: string, offset: number, length: number): Promise<Uint8Array | null> {
  const file = await fileOf(path);
  return file ? new Uint8Array(await file.slice(offset, offset + length).arrayBuffer()) : null;
}

async function listDir(path: string): Promise<string[] | null> {
  if (!source) return null;
  if (source.kind === "files") {
    const prefix = path + "/";
    return [...source.files.keys()]
      .filter((k) => k.startsWith(prefix) && !k.slice(prefix.length).includes("/"))
      .map((k) => k.slice(prefix.length));
  }
  try {
    const dir = await dirOf(source.handle, path.split("/"));
    const names: string[] = [];
    for await (const [name, h] of dir.entries()) if (h.kind === "file") names.push(name);
    return names;
  } catch (err) {
    if (missing(err)) return null;
    throw err;
  }
}

const message = (err: unknown) => (err instanceof Error ? err.message : String(err));
const reply = (r: Reply) => self.postMessage(r);

self.onmessage = async ({ data: msg }: MessageEvent<Request>) => {
  try {
    await ready;
    if (msg.op === "open") {
      source = msg.source;
      dirs.clear();
      reply({ id: msg.id, info: JSON.parse(await shotdb.open(readFile, readRange, listDir)) });
    } else if (msg.op === "rounds") {
      reply({ id: msg.id, rounds: JSON.parse(await shotdb.rounds(msg.shooter)) });
    } else {
      const results: TargetResult[] = [];
      for (const target of msg.targets) {
        try {
          results.push(JSON.parse(await shotdb.simulate(msg.shooter, target, msg.round)));
        } catch (err) {
          results.push({ target, error: message(err) });
        }
      }
      reply({ id: msg.id, results });
    }
  } catch (err) {
    reply({ id: msg.id, error: message(err) });
  }
};
