// Runs the manager (manager.wasm): it reads the chosen folder and runs the shot queue.
// The page sends it the user's actions and a MessagePort to each compute worker.
// manager.wasm and wasm_exec.js sit next to the bundled worker in dist/.
import type { Source, ToManager } from "./types";

declare const self: DedicatedWorkerGlobalScope;

const message = (err: unknown) => (err instanceof Error ? err.message : String(err));
const fatal = (error: string) => self.postMessage(JSON.stringify({ op: "fatal", error }));

let source: Source | null = null;
const dirs = new Map<string, FileSystemDirectoryHandle>(); // directory path -> handle
const files = new Map<string, File>(); // path -> file of a handle source, as getFile is slow

function useSource(s: Source) {
  source = s;
  dirs.clear();
  files.clear();
}

function missing(err: unknown): boolean {
  return err instanceof DOMException && (err.name === "NotFoundError" || err.name === "TypeMismatchError");
}

async function fileOf(path: string): Promise<File | null> {
  if (!source) return null;
  if (source.kind === "files") return source.files.get(path) ?? null;
  const known = files.get(path);
  if (known) return known;
  const parts = path.split("/");
  const name = parts.pop()!;
  try {
    const dir = await dirOf(source.handle, parts);
    const file = await (await dir.getFileHandle(name)).getFile();
    files.set(path, file);
    return file;
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

async function blob(path: string, offset: number, length: number): Promise<Blob | null> {
  const file = await fileOf(path);
  return file ? file.slice(offset, offset + length) : null;
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

const ready = import(new URL("wasm_exec.js", self.location.href).href).then(async () => {
  const go = new Go();
  const { instance } = await WebAssembly.instantiateStreaming(fetch("manager.wasm"), go.importObject);
  void go.run(instance).then(() => fatal("the manager stopped"));
  manager.start(readFile, readRange, listDir, useSource, blob);
});
ready.catch((err) => fatal(message(err)));

self.onmessage = ({ data }: MessageEvent<ToManager>) => {
  void ready.then(() => {
    try {
      manager.message(data);
    } catch (err) {
      fatal(message(err));
    }
  });
};
