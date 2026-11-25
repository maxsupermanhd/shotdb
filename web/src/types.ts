// Shapes of the JSON from cmd/wasm and of the worker messages.

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

export interface Shell {
  name: string;
  type: string;
  pen: number;
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

export type Source = { kind: "handle"; handle: FileSystemDirectoryHandle };

export type Request =
  | { id: number; op: "open"; source: Source }
  | { id: number; op: "sim"; shooter: string; targets: string[] };

export interface Replies {
  open: { info: GameInfo };
  sim: { results: TargetResult[] };
}

export type Reply = { id: number } & ({ error: string } | Replies["open"] | Replies["sim"]);
