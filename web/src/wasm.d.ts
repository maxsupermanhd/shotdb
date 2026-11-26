// The Go runtime from wasm_exec.js and the APIs that cmd/manager and cmd/compute set on globalThis.

declare class Go {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}

// Each function gives null when the file or directory does not exist.
type ReadFile = (path: string) => Promise<Uint8Array | null>;
type ReadRange = (path: string, offset: number, length: number) => Promise<Uint8Array | null>;
type ListDir = (path: string) => Promise<string[] | null>;
// A Blob of the range, which reads nothing until asked
type BlobOf = (path: string, offset: number, length: number) => Promise<Blob | null>;

declare var manager: {
  start(readFile: ReadFile, readRange: ReadRange, listDir: ListDir, useSource: (source: import("./types").Source) => void, blob: BlobOf): void;
  message(msg: import("./types").ToManager): void;
};

// Each gives an error message, "" if none, or undefined when the Go program exits in the call.
declare var compute: {
  tables(packet: Uint8Array): string | undefined;
  shooter(packet: Uint8Array, round: number, sample: number): string | undefined; // round -1: the best of all
  // JSON of a SimResult or a SimError
  simulate(packet: Uint8Array, collision: Uint8Array): string | undefined;
};
