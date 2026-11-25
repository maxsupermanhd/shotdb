// The Go runtime from wasm_exec.js and the API that cmd/wasm sets on globalThis.

declare class Go {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}

// Each function gives null when the file or directory does not exist.
type ReadFile = (path: string) => Promise<Uint8Array | null>;
type ReadRange = (path: string, offset: number, length: number) => Promise<Uint8Array | null>;
type ListDir = (path: string) => Promise<string[] | null>;

declare var shotdb: {
  // JSON of a GameInfo
  open(readFile: ReadFile, readRange: ReadRange, listDir: ListDir): Promise<string>;
  // JSON of a SimResult
  simulate(shooter: string, target: string): Promise<string>;
};
