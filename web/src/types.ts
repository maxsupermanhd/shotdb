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
  seatChange: number; // s for a crew member to take the gunner's seat, with the best crew: the game's value
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
  follow?: Follow;
}

// The shots after the first in a view. A zero count is left out.
export interface Follow {
  lines?: number; // first shots that penetrate, damage crew and do not kill
  kills?: number; // of those, lines that a follow-up kills
  shots?: number; // sum over the kills of the follow-ups fired
  seconds?: number; // sum over the kills of the time to the kill
  pairs?: number; // two-shot combinations in one direction
  pairKills?: number;
  silenced?: number; // first shots after which the target cannot fire back
  third?: number; // of those, lines with a third shot before the target fires
  thirdKills?: number; // of those, lines killed by the second or third shot
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
  followUps?: number; // shots after the first in the window of Options, at least 1; none for a guided round
  followSec?: number; // time of the last follow-up, s
  thirdSec?: number; // time of the third shot, s
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
  extra?: number; // follow-up and pair shots
  each?: RoundResult[]; // when all rounds are fired: the result of each round, by its index
}

// One round of a SimResult with all rounds. The same round fired alone gives the same views, score and shots.
export interface RoundResult {
  views: View[];
  score: number;
  ms: number;
  shots: number;
  extra?: number;
}

export interface SimError {
  target: string;
  error: string;
}

export type TargetResult = SimResult | SimError;

export type Source =
  | { kind: "handle"; handle: FileSystemDirectoryHandle }
  | { kind: "files"; files: Map<string, File> };

// The model settings, as sim.Options. The manager gives the defaults in "samples".
export interface Options {
  followUps: boolean;
  pairs: boolean;
  returnFire: boolean;
  window: number; // s after the first shot in which the follow-ups fall
  stock: boolean; // a manual loader is slower
  lastRound: boolean; // the first shot fires the last round of the magazine
  seatChange: number; // s to take the gunner's seat; less than 0 for the game's value
  override: boolean; // a live secondGunnerDm crew part fires the gun for a dead gunner
  drives: boolean; // a dead turret drive stops the gun
  outside: boolean; // a hit on an outside part is a pen, also when the shell then stops on the armour
}

// Page -> manager. See cmd/manager.
export type ToManager =
  | { op: "open"; id: number; source: Source }
  | { op: "run"; shooter: string; round: number; lines: number; options: string } // options: the JSON of Options
  | { op: "go"; gen: number; done: string[] } // start queue gen; done: the targets that the page has results for
  | { op: "pause" }
  | { op: "hold"; n: number }
  | { op: "worker"; id: number; port: MessagePort }
  | { op: "drop"; id: number }
  | { op: "failed"; id: number; error: string; gen?: number; target?: string };

// What the user picked. lines: shot lines a round on each target, one of the counts of "samples".
export interface Choice {
  shooter: string; // "" for none
  round: number; // the only round to fire, or -1 for the best of all
  lines: number;
  opts: Options;
}

export interface Progress {
  done: number; // also the targets that the page had results for
  total: number;
  running: number; // targets that the compute workers hold
  paused: boolean;
  at?: number; // BR of the next target
}

// Manager -> page, as JSON strings. gen is the queue that the message is about.
export type FromManager =
  | { op: "samples"; lines: number[]; default: number; options: Options } // the line counts that "run" takes, and the default model
  | { op: "opened"; id: number; info?: GameInfo; error?: string }
  // a new queue, which waits for "go"; source and version of the game data when the manager has data
  | { op: "queue"; gen: number; shooter: string; lines: number; options: Options; source?: string; version?: string; error?: string; round?: number; range?: number; rounds?: Shell[]; targets?: string[] }
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
  | { op: "shooter"; gen: number; round: number; sample: number; options: string; buf: Uint8Array } // sample: an index in sim.Samples; options: the JSON of Options
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
