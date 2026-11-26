// Shapes of the JSON from the manager and of the worker messages.

export interface Unit {
  id: string;
  name: string;
  country: string;
  role: string;
  rank: number;
  br: number;
}

export interface Card {
  id: string;
  prem?: boolean;
  squad?: boolean;
}

// rows[row][col] is a stack of cards, or null for an empty cell.
export interface Grid {
  cols: number;
  rows: (Card[] | null)[][] | null;
}

export interface Rank {
  n: number;
  research: Grid;
  premium: Grid;
}

export interface Tree {
  country: string;
  name: string;
  ranks: Rank[];
}

export interface GameInfo {
  source: string;
  version: string;
  units: Unit[];
  trees: Tree[];
}

export type Outcome = "kill" | "pen" | "shatter" | "ricochet" | "nonpen";

export interface Plate {
  name: string;
  mm: number;
  shots: number;
}

export interface View {
  name: string;
  counts: Partial<Record<Outcome, number>>;
  score: number;
  plates: Plate[];
}

// A round of the shooter; index is its place in Rounds.rounds.
export interface Shell {
  index: number;
  name: string;
  gun: string;
  type: string;
  caliber: number; // mm
  mass: number; // kg
  speed: number; // m/s at the muzzle
  pen: number; // mm flat at the range
  filler: number; // kg, 0 if none
  explosive: string;
}

export interface Rounds {
  range: number; // m
  rounds: Shell[];
}

export interface SimResult {
  shooter: string;
  target: string;
  shell: Shell | null; // null when the shooter has no usable gun
  views: View[];
  score: number;
  ms: number;
  shots: number;
}

export interface SimError {
  target: string;
  error: string;
}

export type TargetResult = SimResult | SimError;

export type Source =
  | { kind: "handle"; handle: FileSystemDirectoryHandle }
  | { kind: "files"; files: Map<string, File> };

// Page -> manager. See cmd/manager.
export type ToManager =
  | { op: "open"; id: number; source: Source }
  | { op: "shooter"; id: string }
  | { op: "round"; index: number }
  | { op: "pause" }
  | { op: "hold"; n: number }
  | { op: "worker"; id: number; port: MessagePort }
  | { op: "drop"; id: number }
  | { op: "failed"; id: number; error: string; gen?: number; target?: string };

export interface Progress {
  done: number;
  total: number;
  running: number; // targets that the compute workers hold
  ms: number; // simulation time of the done targets
  paused: boolean;
  at?: number; // BR of the next target
  wins: number[] | null; // targets per round that the round was best on
}

// Manager -> page, as JSON strings. gen is the queue that the message is about.
export type FromManager =
  | { op: "opened"; id: number; info?: GameInfo; error?: string }
  | { op: "queue"; gen: number; shooter: string; error?: string; round?: number; range?: number; rounds?: Shell[]; targets?: string[] }
  | { op: "start"; gen: number; target: string }
  | { op: "back"; gen: number; targets: string[] } // targets that went back in the queue
  | ({ op: "result"; gen: number; target: string } & ({ result: SimResult } | { error: string }))
  | ({ op: "progress"; gen: number } & Progress)
  | { op: "error"; error: string }
  | { op: "fatal"; error: string };

// Manager -> compute worker, over its port. buf is a packet of package packet, blob the target's collision resource.
export type ToCompute =
  | { op: "gen"; gen: number }
  | { op: "tables"; buf: Uint8Array }
  | { op: "shooter"; gen: number; round: number; buf: Uint8Array }
  | { op: "target"; gen: number; target: string; buf: Uint8Array; blob: Blob };

// Compute worker -> manager: the JSON of a SimResult or a SimError, or no result when the target was skipped.
export interface FromCompute {
  gen: number;
  target: string;
  result?: string;
}

// Compute worker -> page when it fails. gen and target name the shot that it ran, if one.
export interface ComputeFatal {
  op: "fatal";
  error: string;
  gen?: number;
  target?: string;
}
