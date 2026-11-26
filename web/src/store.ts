// IndexedDB "shotdb": the remembered folder and the shot results of the runs before.
//   folder   "game" -> FileSystemDirectoryHandle
//   results  [run, target] -> Stored
//   runs     run -> RunInfo
// A run is the JSON of all that changes a result (see runKey in app.ts). A result stays valid for TTL.
// When the runs take more than MAX_BYTES or are more than MAX_RUNS, the runs used least recently go first.

export const TTL = 24 * 3600 * 1000;
const MAX_BYTES = 100e6, MAX_RUNS = 40;

type Stored = { t: number; s: string }; // time of the result, JSON of the SimResult
type RunInfo = { used: number; last: number; bytes: number }; // last: time of the newest result

let db: Promise<IDBDatabase> | null = null;

function open() {
  db ??= new Promise<IDBDatabase>((resolve, reject) => {
    const req = indexedDB.open("shotdb", 2);
    req.onupgradeneeded = () => {
      for (const name of ["folder", "results", "runs"]) {
        if (!req.result.objectStoreNames.contains(name)) req.result.createObjectStore(name);
      }
    };
    req.onerror = () => reject(req.error);
    req.onsuccess = () => {
      const d = req.result;
      // a newer page in another tab upgrades the database
      d.onversionchange = () => {
        d.close();
        db = null;
      };
      resolve(d);
    };
  });
  db.catch(() => (db = null));
  return db;
}

// Runs work on the stores in one transaction and gives what work gives when the transaction is complete.
async function tx<T>(stores: string[], mode: IDBTransactionMode, work: (t: IDBTransaction) => T | Promise<T>): Promise<T> {
  const t = (await open()).transaction(stores, mode);
  const done = new Promise<void>((ok, fail) => {
    t.oncomplete = () => ok();
    t.onerror = t.onabort = () => fail(t.error);
  });
  const v = await work(t);
  await done;
  return v;
}

const get = <T>(r: IDBRequest<T>) => new Promise<T>((ok, fail) => {
  r.onsuccess = () => ok(r.result);
  r.onerror = () => fail(r.error);
});

const range = (run: string) => IDBKeyRange.bound([run], [run, []]);

export function folder(op: "get" | "put" | "delete", handle?: FileSystemDirectoryHandle) {
  return tx(["folder"], op === "get" ? "readonly" : "readwrite", (t) => {
    const store = t.objectStore("folder");
    const r: IDBRequest = op === "get" ? store.get("game") : op === "put" ? store.put(handle, "game") : store.delete("game");
    return get<FileSystemDirectoryHandle | undefined>(r);
  });
}

// The valid results of a run, as target -> JSON. It marks the run as used.
export function results(run: string): Promise<Map<string, string>> {
  return tx(["results", "runs"], "readwrite", async (t) => {
    const store = t.objectStore("results");
    const runs = t.objectStore("runs");
    const [keys, values, info] = await Promise.all([
      get(store.getAllKeys(range(run))), get(store.getAll(range(run))), get<RunInfo | undefined>(runs.get(run)),
    ]);
    const out = new Map<string, string>();
    const old = Date.now() - TTL;
    values.forEach((v: Stored, i) => {
      if (v.t > old) out.set((keys[i] as string[])[1], v.s);
    });
    if (info) runs.put({ ...info, used: Date.now() }, run);
    return out;
  });
}

// Each result goes in its own transaction, so that a read of the run after it sees it.
export function put(run: string, target: string, s: string) {
  return tx(["results", "runs"], "readwrite", async (t) => {
    const store = t.objectStore("results");
    const runs = t.objectStore("runs");
    const [old, info] = await Promise.all([get<Stored | undefined>(store.get([run, target])), get<RunInfo | undefined>(runs.get(run))]);
    const now = Date.now();
    store.put({ t: now, s } satisfies Stored, [run, target]);
    runs.put({ used: now, last: now, bytes: (info?.bytes ?? 0) - (old?.s.length ?? 0) + s.length } satisfies RunInfo, run);
  });
}

// Deletes the runs with no valid result, then the runs used least recently while there are too many
// or they take too much. The run keep stays.
export function prune(keep: string) {
  return tx(["results", "runs"], "readwrite", async (t) => {
    const store = t.objectStore("results");
    const runs = t.objectStore("runs");
    const [keys, infos] = await Promise.all([get(runs.getAllKeys()), get<RunInfo[]>(runs.getAll())]);
    const old = Date.now() - TTL;
    const drop = (run: string) => {
      store.delete(range(run));
      runs.delete(run);
    };
    const live: { run: string; info: RunInfo }[] = [];
    keys.forEach((k, i) => (infos[i].last <= old && k !== keep ? drop(k as string) : live.push({ run: k as string, info: infos[i] })));
    live.sort((a, b) => a.info.used - b.info.used);
    let bytes = live.reduce((a, r) => a + r.info.bytes, 0);
    let n = live.length;
    for (const r of live) {
      if (n <= MAX_RUNS && bytes <= MAX_BYTES) break;
      if (r.run === keep) continue;
      drop(r.run);
      bytes -= r.info.bytes;
      n--;
    }
  });
}
