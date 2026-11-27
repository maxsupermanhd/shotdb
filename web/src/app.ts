// Tech trees and names from the opened game data, unit icons from the wiki's static content; shot results from the Go simulation.
// The manager worker (manager.wasm) reads the game files and runs the queue. It hands the targets to a pool of compute
// workers (compute.wasm) over MessagePorts. The page only draws and passes the user's actions to the manager.
import * as store from "./store";
import type { Card, Choice, ComputeFatal, Follow, FromManager, GameInfo, Grid, Options, Outcome, Plate, Progress, Rounds, Shell, SimResult, Source, TargetResult, ToManager, Tree, Unit, View } from "./types";

const RANKS = ["I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X"];
const OUTCOMES: Outcome[] = ["kill", "pen", "shatter", "ricochet", "nonpen"];
const OUTCOME_LABEL: Record<Outcome, string> = { kill: "Kill", pen: "Pen", shatter: "Core broke", ricochet: "Ricochet", nonpen: "No pen" };
const VIEW_WEIGHT = [0.5, 0.3, 0.2]; // front, side, rear, as sim.Views
const TILE_MIN = 8, TILE_MAX = 70; // TILE_MAX: the height of the unit pictures and the view bars

const params = new URLSearchParams(location.search);
const nationsParam = params.get("nations");
const state = {
  shooter: "", // none until the user picks one
  nations: nationsParam ? new Set(nationsParam.split(",")) : null as Set<string> | null, // null: all
  // "vertical": the shown trees side by side; "horizontal": each tree on its side, one under the other;
  // "compact": the vertical view with small square tiles; "nation": one tree
  view: firstView(),
  nation: params.get("nation") ?? localStorage.getItem("shotdb.nation") ?? "",
  game: new Map<string, Unit>(), // from the opened game data
  results: new Map<string, TargetResult>(),
  kept: new Set<string>(), // the results that came from the store
  rounds: null as Rounds | null, // of the shooter
  round: -1, // the only round to fire, or -1 for the best of all
  lines: 0, // shot lines a round on each target, in the shown queue
  opts: null as Options | null, // the model of the shown queue
  queue: [] as string[], // in the manager's order
  started: new Set<string>(), // targets that a compute worker holds
  gen: 0, // the manager's queue; messages of other queues are dropped
  run: "", // the key of the queue's results in the store, "" when they are not kept
  data: { source: "", version: "" }, // of the game data of the queue
  progress: { done: 0, total: 0, running: 0, paused: false } as Progress,
  stopped: "", // why the queue stopped, when the manager failed
  byLabel: new Map<string, string>(),
};

type View = "vertical" | "horizontal" | "compact" | "nation";
// The vertical view had the name "all" before.
function firstView(): View {
  const v = params.get("view") ?? localStorage.getItem("shotdb.view");
  if (v === "horizontal" || v === "compact" || v === "nation") return v;
  // Compact was a toggle of its own before, on top of the view of all trees.
  return !params.has("view") && localStorage.getItem("shotdb.compact") === "1" ? "compact" : "vertical";
}

const $ = <T extends HTMLElement = HTMLElement>(s: string) => document.querySelector<T>(s)!;
// WT font glyphs (origin and event markers) that a normal font cannot draw
const clean = (name: string) => name.replace(/[␀-◿-]/g, "").trim();
const message = (err: unknown) => (err instanceof Error ? err.message : String(err));
// localStorage can refuse a value, for example when it is full or turned off. The setting then holds only for this visit.
function save(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch (err) {
    console.warn(`cannot keep ${key}: ${message(err)}`);
  }
}
const MODEL_KEY = "shotdb.model"; // the model settings that differ from the defaults
let defaults: Options | null = null; // the model of the manager, from "samples"
function savedModel(): Partial<Options> {
  try {
    return JSON.parse(localStorage.getItem(MODEL_KEY) ?? "{}");
  } catch {
    return {};
  }
}

let manager: Worker;
let managerDown = false;
const tell = (msg: ToManager, transfer: Transferable[] = []) => manager.postMessage(msg, transfer);
// A new manager has no game data and no compute workers.
function startManager() {
  managerDown = false;
  manager = new Worker(new URL("./manager.js", import.meta.url), { type: "module" });
  manager.onmessage = ({ data }: MessageEvent<string>) => onManager(JSON.parse(data));
  manager.onerror = (e) => managerFailed(e.message || "the manager did not load");
  // speed: targets that a compute worker holds at once, one runs and the others wait; the manager keeps 2 for a bad value
  tell({ op: "hold", n: Number(params.get("speed") ?? 2) });
  // The choice before a failure goes to the new manager: it keeps it for the next open.
  // The manager keeps its default for a count that it does not have.
  tell({ op: "run", shooter: state.shooter, round: state.round, lines: state.lines || Number(localStorage.getItem("shotdb.lines")), options: JSON.stringify(state.opts ?? savedModel()) });
}
startManager();

// Each compute worker keeps the tables, the shooter and the target that it runs, and nothing more.
type Peer = { id: number; worker: Worker };
const pool: Peer[] = [];
let peerId = 0;
// A failed worker gets a new one, but only RESTARTS times in a row with no good result between.
const RESTARTS = 3;
let restarts = 0;
const cores = navigator.hardwareConcurrency || 2;
setupWorkers();

let trees: Tree[] = [];
const cards = new Map<string, HTMLElement[]>(); // unit id -> card elements
// Icons load when a card comes near the view.
const icons = new IntersectionObserver((entries) => {
  for (const e of entries) {
    if (!e.isIntersecting) continue;
    icons.unobserve(e.target);
    loadIcon(e.target as HTMLElement);
  }
}, { root: $("#trees").parentElement, rootMargin: "400px" });
setupTrees();
setupLines();
setupModel();
setupView();
setupPicker();
setupConfirm();
setupSettings();
setupOpen();

// Default: half of the cores, at most 8.
function setupWorkers() {
  const select = $<HTMLSelectElement>("#workers");
  for (let n = 1; n <= cores; n++) select.add(new Option(String(n)));
  const stored = Number(localStorage.getItem("shotdb.workers"));
  select.value = String(stored >= 1 && stored <= cores ? stored : Math.min(8, Math.max(1, Math.floor(cores / 2))));
  select.addEventListener("change", () => {
    save("shotdb.workers", select.value);
    restarts = 0;
    setWorkers(Number(select.value));
  });
  setWorkers(Number(select.value));
}

// The manager gives the counts in its first message, "samples". A change starts the queue of the shooter again.
function setupLines() {
  const select = $<HTMLSelectElement>("#lines");
  select.addEventListener("change", () => {
    const n = Number(select.value);
    choose({ shooter: state.shooter, round: state.round, lines: n, opts: model() }, `Fire ${n} lines a round?`);
  });
}

// The model settings. A change starts the queue of the shooter again, as a change of Shots does.
function setupModel() {
  const ask = () => {
    const o = model();
    // A bad value goes back to the model that Run starts, or to the shown one.
    if (!(o.window >= 0 && o.window <= 10) || !(o.seatChange === -1 || o.seatChange >= 0 && o.seatChange <= 60)) return fillModel(asking?.c.opts ?? shownModel());
    modelState(o);
    choose({ shooter: state.shooter, round: state.round, lines: lines(), opts: o }, "Run with the new model?", "#model-slot");
  };
  $("#model").addEventListener("change", ask);
  $("#model-reset").addEventListener("click", () => {
    if (!defaults) return;
    fillModel(defaults);
    ask();
  });
}

const modelFields = () => document.querySelectorAll<HTMLInputElement | HTMLSelectElement>("#model [data-opt]");
const field = (k: keyof Options) => $<HTMLInputElement | HTMLSelectElement>(`#model [data-opt="${k}"]`);
// the model of the shown queue, or before the first queue the kept one
const shownModel = (): Options => state.opts ?? { ...defaults!, ...savedModel() };
// Puts Shots and the model back to the shown queue.
function applied() {
  if (state.lines) $<HTMLSelectElement>("#lines").value = String(state.lines);
  fillModel(shownModel());
}

// the model in the settings, in the key order of the defaults
function model(): Options {
  const o: Record<string, boolean | number> = { ...(defaults ?? shownModel()) };
  for (const el of modelFields()) {
    const k = el.dataset.opt!;
    if (el instanceof HTMLSelectElement) o[k] = el.value === "1";
    else if (el.type === "checkbox") o[k] = el.checked;
    else o[k] = el.value.trim() === "" ? (k === "seatChange" ? -1 : NaN) : Number(el.value);
  }
  return o as unknown as Options;
}

function fillModel(o: Options) {
  for (const el of modelFields()) {
    const v = o[el.dataset.opt as keyof Options];
    if (el instanceof HTMLSelectElement) el.value = v ? "1" : "0";
    else if (el.type === "checkbox") el.checked = v as boolean;
    else el.value = typeof v === "number" && v >= 0 ? String(v) : "";
  }
  modelState(o);
}

// A setting that has no effect with the other settings is off.
function modelState(o: Options) {
  field("window").disabled = !o.followUps;
  field("stock").disabled = field("lastRound").disabled = !o.followUps && !o.returnFire;
  field("seatChange").disabled = field("override").disabled = field("drives").disabled = !o.returnFire;
}

// The legend of the gain bar shows the gains of the shown queue.
function legend(o: Options) {
  $("#legend-follow").hidden = !o.followUps;
  $("#legend-pairs").hidden = !o.pairs;
  $(".legend-gain").hidden = !o.followUps && !o.pairs;
}

// Only the settings that differ from the defaults are kept, so that a new default reaches them.
function run(c: Choice) {
  if (c.lines) save("shotdb.lines", String(c.lines));
  const changed = Object.entries(c.opts).filter(([k, v]) => !defaults || defaults[k as keyof Options] !== v);
  save(MODEL_KEY, JSON.stringify(Object.fromEntries(changed)));
  tell({ op: "run", shooter: c.shooter, round: c.round, lines: c.lines, options: JSON.stringify(c.opts) });
}
// the count in the Shots box
const lines = () => Number($<HTMLSelectElement>("#lines").value) || state.lines;

// A dropped worker stops at once: the manager puts its targets back in the queue.
function setWorkers(n: number) {
  while (pool.length > n) {
    const peer = pool.pop()!;
    tell({ op: "drop", id: peer.id });
    peer.worker.terminate();
  }
  while (pool.length < n) {
    const id = ++peerId;
    const worker = new Worker(new URL("./compute.js", import.meta.url), { type: "module" });
    const { port1, port2 } = new MessageChannel();
    worker.postMessage({ port: port2 }, [port2]);
    tell({ op: "worker", id, port: port1 }, [port1]);
    const fail = ({ error, gen, target }: ComputeFatal) => {
      const i = pool.findIndex((p) => p.id === id);
      if (i < 0) return;
      pool.splice(i, 1);
      worker.terminate();
      tell({ op: "failed", id, error, gen, target });
      if (restarts < RESTARTS) {
        restarts++;
        setWorkers(Number($<HTMLSelectElement>("#workers").value));
        setStatus(`A compute worker failed: ${error}. A new one started.`, true);
      } else {
        setStatus(`A compute worker failed: ${error}. ${RESTARTS} failed in a row, so no new one started. ${pool.length} left.`, true);
      }
      progress();
    };
    worker.onerror = (e) => {
      e.preventDefault();
      fail({ op: "fatal", error: e.message || "the worker did not load" });
    };
    worker.onmessage = ({ data }: MessageEvent<ComputeFatal>) => fail(data);
    pool.push({ id, worker });
  }
}

type Opened = { info?: GameInfo; error?: string };
let openSeq = 0; // the id of the newest open
const opens = new Map<number, (r: Opened) => void>(); // open id -> its resolve
// The data of an older open that the manager holds while a newer open loads. The page shows it when the newer open fails.
let held: { info: GameInfo; src: Source; label: string } | null = null;
let shown: { src: Source; label: string } | null = null; // the source of the trees on the page
let pinned = false; // the hover parameter's tooltip is shown

function onManager(msg: FromManager) {
  switch (msg.op) {
    case "samples": {
      const select = $<HTMLSelectElement>("#lines");
      select.replaceChildren(...msg.lines.map((n) => new Option(String(n))));
      const stored = Number(localStorage.getItem("shotdb.lines"));
      select.value = String(msg.lines.includes(stored) ? stored : msg.default);
      defaults = msg.options;
      fillModel(shownModel());
      legend(shownModel());
      return;
    }
    case "opened":
      opens.get(msg.id)?.(msg);
      opens.delete(msg.id);
      return;
    case "error":
      return setStatus(msg.error, true);
    case "fatal":
      return managerFailed(msg.error);
    case "queue":
      return newQueue(msg);
  }
  if (msg.gen !== state.gen) return;
  switch (msg.op) {
    case "start":
      state.started.add(msg.target);
      cards.get(msg.target)?.forEach((el) => el.classList.add("running"));
      break;
    case "back":
      for (const id of msg.targets) {
        state.started.delete(id);
        cards.get(id)?.forEach((el) => el.classList.remove("running"));
      }
      break;
    case "result":
      state.started.delete(msg.target);
      if ("result" in msg) {
        restarts = 0;
        // errors are not kept: the next run tries again
        if (state.run) store.put(state.run, msg.target, JSON.stringify(msg.result)).catch((err) => console.warn(`cannot keep the result: ${message(err)}`));
        delete msg.result.each; // only the store needs it
      }
      state.results.set(msg.target, "result" in msg ? msg.result : { target: msg.target, error: msg.error });
      paint(msg.target);
      break;
    case "progress": {
      const { op: _op, gen: _gen, ...p } = msg;
      state.progress = p;
      progress();
      if (p.total && p.done >= p.total && !pinned) pinHover();
      break;
    }
  }
}

// The manager failed: the targets that it gave out get no result, so they fail here.
// The compute workers stop too, as their ports go to the failed manager. The next open starts new ones.
function managerFailed(error: string) {
  if (managerDown) return;
  managerDown = true;
  manager.terminate();
  for (const peer of pool.splice(0)) peer.worker.terminate();
  for (const resolve of opens.values()) resolve({ error: `The manager failed: ${error}` });
  opens.clear();
  setStatus(`The manager failed: ${error}. Open a folder to start it again.`, true);
  state.stopped = error;
  for (const id of state.started) {
    state.results.set(id, { target: id, error: `the manager failed: ${error}` });
    paint(id);
  }
  state.started.clear();
  progress();
}

function setupTrees() {
  const main = $("#trees");
  const tip = $("#tooltip");
  main.addEventListener("click", (e) => {
    const card = (e.target as Element).closest<HTMLElement>(".card[data-id]");
    const id = card?.dataset.id;
    if (id && shootable(id)) choose({ shooter: id, round: id === state.shooter ? state.round : -1, lines: lines(), opts: model() }, `Shoot with ${name(id)}?`);
  });
  // The scroll to the pinned card makes the browser send mouse events without a move. Only a real move ends the pin.
  const isPinned = () => tip.classList.contains("pinned");
  addEventListener("pointermove", (e) => {
    if (e.movementX || e.movementY) tip.classList.remove("pinned");
  }, { passive: true });
  main.addEventListener("mouseover", (e) => {
    const card = (e.target as Element).closest<HTMLElement>("[data-id]");
    if (card && !isPinned()) showTooltip(card);
  });
  main.addEventListener("mouseleave", () => isPinned() || hideTip());
  main.parentElement!.addEventListener("scroll", () => isPinned() || hideTip(), { passive: true });
  main.addEventListener("mouseout", (e) => {
    if (!isPinned() && !(e.relatedTarget instanceof Element && e.relatedTarget.closest("[data-id]"))) hideTip();
  });
  $("#rounds").addEventListener("click", (e) => {
    const row = (e.target as Element).closest<HTMLElement>(".round");
    if (!row) return;
    const i = Number(row.dataset.index);
    const round = state.round === i ? -1 : i;
    const r = state.rounds!.rounds;
    choose({ shooter: state.shooter, round, lines: lines(), opts: model() }, round < 0 ? `Fire the best of all ${r.length} rounds?` : `Fire only ${r[i].name}?`);
  });
  $("#pause").addEventListener("click", () => tell({ op: "pause" }));
  new ResizeObserver(() => {
    if (!state.rounds?.rounds.length || $(".bar").clientWidth === roundsWidth) return;
    roundsPanel();
    progress();
  }).observe($(".bar"));
}

function render() {
  const main = $("#trees");
  const nav = $("#nations");
  main.replaceChildren();
  nav.replaceChildren();
  cards.clear();
  icons.disconnect();
  for (const tree of trees) {
    const chip = document.createElement("button");
    chip.className = "chip";
    chip.textContent = tree.name;
    chip.dataset.country = tree.country;
    chip.addEventListener("click", () => {
      if (state.view === "nation") return showNation(tree.country);
      state.nations ??= new Set(trees.map((t) => t.country));
      state.nations.has(tree.country) ? state.nations.delete(tree.country) : state.nations.add(tree.country);
      applyNations();
    });
    nav.append(chip);
    if (state.view === "horizontal") continue;

    const head = document.createElement("h2");
    head.className = `nation-head n-${tree.country}`;
    head.innerHTML = `<span>${esc(tree.name)}</span><span class="sub">Researchable</span><span class="sub">Premium</span>`;
    main.append(head);

    for (const rank of tree.ranks) {
      const block = document.createElement("section");
      block.className = `rank n-${tree.country}`;
      block.style.gridRow = String(rank.n + 1);
      block.innerHTML = `<div class="rank-label">Rank ${RANKS[rank.n - 1]}</div>`;
      const body = document.createElement("div");
      body.className = "rank-body";
      body.append(grid(rank.research, "research"), grid(rank.premium, "premium"));
      block.append(body);
      main.append(block);
    }
  }
  if (state.view === "horizontal") horizontal(main);
  // The queue can be older than the trees: the held data of an older open shows after the queue of that open started.
  cards.get(state.shooter)?.forEach((el) => el.classList.add("shooter"));
  for (const id of state.started) cards.get(id)?.forEach((el) => el.classList.add("running"));
  for (const id of state.results.keys()) paint(id);
  applyNations();
  markRuler();
  centerShooter();
}

const NAME_W = 120; // px, the column of the nation names in the horizontal view
const SLOT = 96; // px, a card and the gap after it
const RANK_GAP = 24; // px between two rank columns

// The horizontal view: each nation tree on its side. Each nation is a band, and each research or premium line is
// a row of the band. The columns are the ranks. A rank column is as wide as the widest nation needs at that rank,
// so the ranks line up in all bands. In a rank, the rows of the game tree are the x order. The cards of a folder
// stand side by side, and an x place is as wide as the largest folder at that place in the nation.
function horizontal(main: HTMLElement) {
  const ranks = [...new Set(trees.flatMap((t) => t.ranks.map((r) => r.n)))].sort((a, b) => a - b);
  if (!ranks.length) return;
  const col = new Map(ranks.map((n, i) => [n, i + 2]));
  const places = (g: Grid) => (g.rows ?? []).map((row) => Math.max(0, ...row.map((cell) => cell?.length ?? 0)));
  const width = (g: Grid) => places(g).reduce((a, p) => a + p * SLOT, 0) - 4;
  const rankW = new Map<number, number>();
  for (const tree of trees) {
    for (const r of tree.ranks) rankW.set(r.n, Math.max(rankW.get(r.n) ?? 0, width(r.research), width(r.premium)));
  }
  main.style.setProperty("--hcols", `${NAME_W}px ${ranks.map((n) => `${rankW.get(n)}px`).join(" ")}`);
  main.style.setProperty("--rank-gap", `${RANK_GAP}px`);

  const ruler = document.createElement("div");
  ruler.className = "h-ruler";
  ruler.innerHTML = `<div class="h-corner">Rank</div>` + ranks.map((n) => `<div class="h-rank" data-rank="${n}">Rank ${RANKS[n - 1]}</div>`).join("");
  main.append(ruler);
  for (const tree of trees) {
    // the lines of the tree: the columns of the game tree grids that hold a card in a rank
    const lines = (kind: "research" | "premium") => [...new Set(tree.ranks.flatMap((r) => (r[kind].rows ?? []).flatMap((row) => row.flatMap((cell, c) => (cell?.length ? [c] : [])))))].sort((a, b) => a - b);
    const research = lines("research"), premium = lines("premium");
    const r = research.length, p = premium.length;
    const rows = r + (p ? p + 1 : 0);
    const band = document.createElement("section");
    band.className = `h-band n-${tree.country}`;
    band.innerHTML = ranks.slice(1).map((n) => `<i class="h-guide" style="grid-column:${col.get(n)};grid-row:1 / span ${rows}"></i>`).join("")
      + `<div class="h-name" style="grid-row:1 / span ${Math.max(1, r)}"><span class="h-label"><b>${esc(tree.name)}</b><span class="sub">Researchable</span></span></div>`
      + (p ? `<i class="h-split" style="grid-row:${r + 1}"></i><div class="h-name" style="grid-row:${r + 1} / span ${p + 1}"><span class="h-label"><b>${esc(tree.name)}</b><span class="sub">Premium</span></span></div>` : "");
    for (const rank of tree.ranks) {
      for (const [kind, list, first] of [["research", research, 1], ["premium", premium, r + 2]] as const) {
        const g = rank[kind];
        const template = places(g).map((n) => `${Math.max(0, n * SLOT - 4)}px`).join(" ");
        list.forEach((c, i) => {
          const stacks = (g.rows ?? []).flatMap((row, x) => (row[c]?.length ? [{ x, cell: row[c]! }] : []));
          if (!stacks.length) return;
          const line = document.createElement("div");
          line.className = "h-cell";
          line.style.gridArea = `${first + i} / ${col.get(rank.n)}`;
          line.style.gridTemplateColumns = template;
          for (const { x, cell } of stacks) {
            const stack = document.createElement("div");
            stack.className = "h-stack";
            stack.style.gridColumn = String(x + 1);
            stack.append(...cell.map(card));
            line.append(stack);
          }
          band.append(line);
        });
      }
    }
    main.append(band);
  }
}

function markRuler() {
  const rank = state.game.get(state.shooter)?.rank;
  document.querySelectorAll<HTMLElement>(".h-rank").forEach((el) => el.classList.toggle("at", Number(el.dataset.rank) === rank));
}

// The horizontal view scrolls sideways to the shooter's rank column, right of the nation names. When the column is
// wider than the view, the shooter's card goes to the middle of the view, or the column start comes to the left
// when the shooter's nation is hidden. The vertical position does not change. With no shooter the view goes to the left end.
function centerShooter() {
  if (state.view !== "horizontal") return;
  const view = $("main.trees");
  const behavior: ScrollBehavior = still.matches ? "instant" : "smooth";
  const label = document.querySelector<HTMLElement>(`.h-rank[data-rank="${state.game.get(state.shooter)?.rank}"]`);
  if (!label) return view.scrollTo({ left: 0, behavior });
  const room = view.clientWidth - NAME_W;
  const start = label.offsetLeft - NAME_W - RANK_GAP / 2;
  const w = label.offsetWidth + RANK_GAP;
  let left = start + (w - room) / 2;
  if (w > room) {
    const el = cards.get(state.shooter)?.find((c) => c.offsetParent);
    const x = el ? el.getBoundingClientRect().left + el.offsetWidth / 2 - $("#trees").getBoundingClientRect().left : 0;
    left = el ? Math.min(Math.max(x - NAME_W - room / 2, start), start + w - room) : start;
  }
  view.scrollTo({ left, behavior });
}

function grid({ cols, rows }: Grid, kind: string) {
  const table = document.createElement("table");
  table.className = `grid ${kind}`;
  for (const row of rows ?? []) {
    const tr = table.insertRow();
    for (let c = 0; c < cols; c++) {
      const td = tr.insertCell();
      const cell = row[c];
      if (!cell) continue;
      const stack = document.createElement("div");
      stack.className = "stack";
      stack.append(...cell.map(card));
      td.append(stack);
    }
  }
  return table;
}

function card(cell: Card) {
  const el = document.createElement("div");
  el.className = `card${cell.prem ? " prem" : ""}${cell.squad ? " squad" : ""}`;
  el.dataset.id = cell.id;
  el.innerHTML = `<span class="card-name">${esc(name(cell.id))}</span><span class="card-br"></span>`
    + `<span class="card-views"><i></i><i></i><i></i></span><span class="card-gain" hidden><i class="run"></i><i class="dots"></i><i class="tick"></i></span>`;
  el.classList.toggle("nodata", !state.game.has(cell.id));
  el.querySelector(".card-br")!.textContent = shootable(cell.id) ? br(cell.id).toFixed(1) : "no BR";
  track(cell.id, el);
  icons.observe(el);
  return el;
}

function loadIcon(el: HTMLElement) {
  const img = document.createElement("img");
  img.className = "card-icon";
  img.alt = "";
  img.draggable = false; // else a drag of the horizontal view drags the picture
  img.src = `https://static.encyclopedia.warthunder.com/slots/${el.dataset.id}.png`;
  img.addEventListener("error", () => img.remove());
  el.prepend(img);
}

function track(id: string, el: HTMLElement) {
  let els = cards.get(id);
  if (!els) cards.set(id, (els = []));
  els.push(el);
}

function applyNations() {
  const one = state.view === "nation";
  if (one && trees.length && !trees.some((t) => t.country === state.nation)) state.nation = trees[0].country;
  document.body.classList.toggle("one-nation", one);
  document.body.classList.toggle("compact", state.view === "compact");
  document.body.classList.toggle("horizontal", state.view === "horizontal");
  document.querySelectorAll<HTMLElement>(".view-ctl .chip").forEach((b) => {
    b.classList.toggle("on", b.dataset.view === state.view);
    b.setAttribute("aria-pressed", String(b.dataset.view === state.view));
  });
  let col = 0;
  for (const tree of trees) {
    const on = one ? tree.country === state.nation : !state.nations || state.nations.has(tree.country);
    if (on) col++;
    document.querySelectorAll<HTMLElement>(`.n-${tree.country}`).forEach((el) => {
      el.hidden = !on;
      if (state.view !== "horizontal") el.style.gridColumn = String(col);
    });
    const chip = $(`#nations .chip[data-country="${tree.country}"]`);
    chip.classList.toggle("on", on);
    chip.setAttribute("aria-pressed", String(on));
  }
  shooterNation();
  syncUrl();
  fitCompact();
  sides();
}

// The chip of the shooter's nation has a mark of its own, in each view.
function shooterNation() {
  const country = state.game.get(state.shooter)?.country;
  document.querySelectorAll<HTMLElement>("#nations .chip").forEach((chip) => {
    const mark = chip.dataset.country === country;
    chip.classList.toggle("shooter-nation", mark);
    chip.title = mark ? "The nation of the shooter" : "";
  });
}

const still = matchMedia("(prefers-reduced-motion: reduce)");
const SIDE_MIN = 56; // px; under this, the side controls go into the line of nation chips

// the nation at step steps from the shown one, around the end of the list
function neighbour(step: number) {
  const i = trees.findIndex((t) => t.country === state.nation);
  return trees[(i + step + trees.length) % trees.length];
}

// Shows the tree of country in the nation view. The new tree slides in from the side of its chip,
// or from the side of the step (-1 or 1), unless the user asks for reduced motion.
function showNation(country: string, slide = true, step = 0) {
  const from = trees.findIndex((t) => t.country === state.nation);
  const to = trees.findIndex((t) => t.country === country);
  if (to < 0 || country === state.nation) return;
  state.nation = country;
  save("shotdb.nation", country);
  applyNations();
  const view = $("#trees").parentElement!;
  view.scrollTo({ left: 0, top: 0 });
  if (slide && !still.matches) {
    $("#trees").animate([{ transform: `translateX(${(step || to - from) > 0 ? 64 : -64}px)`, opacity: 0 }, { transform: "none", opacity: 1 }],
      { duration: 220, easing: "cubic-bezier(.2, .7, .3, 1)" });
  }
}

// In the nation view, the free space at the left and the right of the tree are the controls for the previous
// and the next nation. They stay in place while the trees scroll.
function sides() {
  const main = $("main.trees");
  const one = state.view === "nation" && trees.length > 1;
  const side = one ? Math.floor((main.clientWidth - $("#trees").offsetWidth) / 2) : 0;
  const wide = side >= SIDE_MIN;
  for (const [id, step] of [["#side-prev", -1], ["#side-next", 1]] as const) {
    const button = $(id);
    button.hidden = !wide;
    if (!wide) continue;
    const name = neighbour(step).name;
    button.style.width = `${side}px`;
    button.style.bottom = `${main.offsetHeight - main.clientHeight}px`;
    button.querySelector(".side-name")!.textContent = name;
    button.title = `${step < 0 ? "Previous" : "Next"} nation: ${name}`;
  }
  $("#side-next").style.right = `${main.offsetWidth - main.clientWidth}px`;
  $("#step-prev").hidden = $("#step-next").hidden = !one || wide;
}

// Vertical, Horizontal, Compact or one nation; in the nation view the left and right arrow keys change the nation.
function setupView() {
  const set = (view: View) => {
    if (view === state.view) return;
    // a change to the nation view starts at the first nation
    if (view === "nation") state.nation = trees[0]?.country ?? "";
    // the horizontal view has cards of its own
    const build = (view === "horizontal") !== (state.view === "horizontal");
    state.view = view;
    save("shotdb.view", view);
    localStorage.removeItem("shotdb.compact");
    hideTip();
    $("#trees").parentElement!.scrollTo({ left: 0, top: 0 });
    if (build) {
      $("#trees").style.removeProperty("--hcols");
      render();
    } else applyNations();
  };
  document.querySelectorAll<HTMLElement>(".view-ctl .chip").forEach((b) => b.addEventListener("click", () => set(b.dataset.view as View)));
  const main = $("main.trees");
  for (const [ids, step] of [[["#side-prev", "#step-prev"], -1], [["#side-next", "#step-next"], 1]] as const) {
    for (const id of ids) $(id).addEventListener("click", () => showNation(neighbour(step).country, true, step));
    // the side controls lie over the view, so the wheel goes on to the trees under them
    $(ids[0]).addEventListener("wheel", (e) => {
      e.preventDefault();
      const k = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? main.clientHeight : 1;
      main.scrollBy(e.deltaX * k, e.deltaY * k);
    }, { passive: false });
  }
  addEventListener("keydown", (e) => {
    if (state.view !== "nation" || !trees.length || asking || $<HTMLDialogElement>("#settings").open || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    if (e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement || e.target instanceof HTMLTextAreaElement) return;
    e.preventDefault();
    const step = e.key === "ArrowRight" ? 1 : -1;
    showNation(neighbour(step).country, true, step);
  });
  // the view changes size with the window and with the header (the rounds panel)
  const resized = new ResizeObserver(() => {
    fitCompact();
    sides();
  });
  resized.observe(main);
  resized.observe($("#trees"));
  setupDrag(main);
  applyNations();
}

const DRAG = 5; // px; a press that moves less is a click

// In the horizontal view, a drag with the left mouse button moves the view. A drag is not a click on a card.
function setupDrag(view: HTMLElement) {
  let drag: { x: number; y: number; left: number; top: number; moved: boolean } | null = null;
  let swallow = false;
  view.addEventListener("pointerdown", (e) => {
    if (state.view !== "horizontal" || e.pointerType !== "mouse" || e.button !== 0) return;
    // a press on the scrollbars is not a drag
    const r = view.getBoundingClientRect();
    if (e.clientX - r.left >= view.clientLeft + view.clientWidth || e.clientY - r.top >= view.clientTop + view.clientHeight) return;
    if ((e.target as Element).closest("button, input, select, a")) return;
    drag = { x: e.clientX, y: e.clientY, left: view.scrollLeft, top: view.scrollTop, moved: false };
  });
  addEventListener("pointermove", (e) => {
    if (!drag) return;
    const dx = e.clientX - drag.x, dy = e.clientY - drag.y;
    if (!drag.moved) {
      if (Math.hypot(dx, dy) < DRAG) return;
      drag.moved = true;
      document.body.classList.add("dragging");
      hideTip();
    }
    view.scrollTo(drag.left - dx, drag.top - dy);
  });
  const end = () => {
    if (drag?.moved) {
      // the click comes after pointerup in the same task
      swallow = true;
      setTimeout(() => (swallow = false));
    }
    drag = null;
    document.body.classList.remove("dragging");
  };
  addEventListener("pointerup", end);
  addEventListener("pointercancel", end);
  view.addEventListener("click", (e) => {
    if (!swallow) return;
    e.stopPropagation();
    e.preventDefault();
  }, true);
}

// The compact grid is only as wide as the shown trees, and its width is linear in the tile
// size. Two trial layouts give the line, which gives the largest tile that fits the view's width.
function fitCompact() {
  const main = $("#trees");
  const note = $("#compact-note");
  if (state.view !== "compact" || !main.querySelector(".nation-head:not([hidden])")) {
    main.style.removeProperty("--tile");
    main.style.removeProperty("--pic");
    note.textContent = "";
    return;
  }
  const width = (s: number) => {
    main.style.setProperty("--tile", `${s}px`);
    return main.offsetWidth;
  };
  const w1 = width(10), w2 = width(20);
  const dw = (w2 - w1) / 10;
  // one pixel spare for rounding; the vertical scrollbar takes its width from the view
  const tile = Math.min(TILE_MAX, Math.floor((main.parentElement!.clientWidth - 1 - (w1 - 10 * dw)) / dw));
  const w = Math.max(TILE_MIN, tile);
  width(w);
  // the view bars take the bottom 4 px of the square
  main.style.setProperty("--pic", `${w - 4}px`);
  note.textContent = tile >= TILE_MIN ? `${w} px` : `does not fit at ${TILE_MIN} px: scroll`;
  note.title = tile >= TILE_MIN ? "" : `The shown trees need tiles below ${TILE_MIN} px to fit the window width. Show fewer nations or use a wider window.`;
}

function setupPicker() {
  const input = $<HTMLInputElement>("#shooter");
  input.addEventListener("change", () => {
    const id = state.byLabel.get(input.value);
    id ? selectShooter(id) : (input.value = state.shooter ? pickerLabel(state.shooter) : "");
  });
  input.addEventListener("focus", () => input.select());
  fillPicker();
}

function fillPicker() {
  const ids = [...cards.keys()].filter(shootable);
  ids.sort((a, b) => name(a).localeCompare(name(b)));
  $("#shooter-list").replaceChildren(...ids.map((id) => {
    const o = document.createElement("option");
    o.value = pickerLabel(id);
    return o;
  }));
  state.byLabel = new Map(ids.map((id) => [pickerLabel(id), id]));
}

let saved: FileSystemDirectoryHandle | null = null; // the remembered folder
// The tooltip of the open button, only in the settings: on the start page the text above the button says it.
let openTip = "The War Thunder install folder (with aces.vromfs.bin) or an unpacked datamine (with aces.vromfs.bin_u)";

function setupOpen() {
  if (!window.showDirectoryPicker) {
    // Firefox: a dropped folder or a folder input. Both give File objects that the page reads in the browser.
    // The input exists only here, so that browsers with the File System Access API never use it.
    const input = document.createElement("input");
    input.type = "file";
    input.webkitdirectory = true;
    input.hidden = true;
    document.body.append(input);
    openTip = "The War Thunder install folder (with aces.vromfs.bin). The browser can call this an upload, "
      + "but the page reads the files on this computer and sends nothing.";
    $("#open").addEventListener("click", () => input.click());
    input.addEventListener("change", async () => {
      const list = [...input.files ?? []];
      input.value = ""; // else the same folder picked again gives no change event
      if (!list.length) return setStatus("The folder has no files: pick the War Thunder install folder", true);
      const root = list[0].webkitRelativePath.split("/")[0];
      const seq = ++readSeq;
      setStatus(`Reading ${root}…`);
      const files = new Map<string, File>();
      for (let i = 0; i < list.length; i++) {
        // An install has thousands of files: give the page time to draw between batches.
        if (i % 2000 === 1999) await new Promise((ok) => setTimeout(ok));
        const path = list[i].webkitRelativePath.slice(root.length + 1);
        const dir = path.slice(0, path.lastIndexOf("/") + 1);
        if (INSTALL_FILES.includes(path) || (dir === `${TANKS}/` && path.endsWith(".grp"))) files.set(path, list[i]);
      }
      await openFiles(seq, files, root);
    });
    addEventListener("dragover", (e) => e.preventDefault());
    addEventListener("drop", async (e) => {
      e.preventDefault();
      const entry = e.dataTransfer?.items[0]?.webkitGetAsEntry();
      if (!entry?.isDirectory) return setStatus("Drop the War Thunder install folder", true);
      const seq = ++readSeq;
      let files: Map<string, File>;
      try {
        setStatus(`Reading ${entry.name}…`);
        files = await installFiles(entry as FileSystemDirectoryEntry);
      } catch (err) {
        if (seq === readSeq) setStatus(`Cannot read ${entry.name}: ${message(err)}`, true);
        return;
      }
      await openFiles(seq, files, entry.name);
    });
    $("#start-text").textContent = "Pick or drop the install folder (with aces.vromfs.bin) on this page. "
      + "The browser can call this an upload, but the page reads the files here and sends nothing. "
      + `${/Firefox\//.test(navigator.userAgent) ? "Firefox" : "This browser"} has no folder picker of the File System Access API `
      + "(showDirectoryPicker), so the page cannot keep access to the folder. Pick the folder again on each visit.";
    return;
  }
  $("#open").addEventListener("click", async () => {
    let handle: FileSystemDirectoryHandle;
    try {
      handle = await window.showDirectoryPicker!({ id: "wt-data", mode: "read" });
    } catch (err) {
      if (!(err instanceof DOMException && err.name === "AbortError")) setStatus(message(err), true);
      return;
    }
    if (await openSource({ kind: "handle", handle }, handle.name)) remember(handle);
  });
  $("#reopen").addEventListener("click", async () => {
    if (!saved) return;
    let perm: PermissionState;
    try {
      perm = await saved.requestPermission({ mode: "read" });
    } catch (err) {
      setStatus(message(err), true);
      return;
    }
    if (perm !== "granted") return setStatus(`No read access to ${saved.name}`, true);
    showSaved(false);
    openSource({ kind: "handle", handle: saved }, saved.name);
  });
  $("#forget").addEventListener("click", () => {
    if (!$("#reopen").hidden) setStatus(""); // the access prompt is for the forgotten folder
    remember(null);
  });
  restore();
}

// The files of an install that the simulation reads, with the model packs TANKS/*.grp. A File reads nothing until asked.
const INSTALL_FILES = ["aces.vromfs.bin", "char.vromfs.bin", "lang.vromfs.bin", "warthunder.yup"];
const TANKS = "content/base/res/tanks";
let readSeq = 0; // the newest dropped or picked folder; an older one that is still read is dropped

// Opens the files of a dropped or picked install folder, if no newer folder came while they were read.
async function openFiles(seq: number, files: Map<string, File>, label: string) {
  if (seq !== readSeq) return;
  if (!files.has("aces.vromfs.bin")) {
    return setStatus(`${label} has no aces.vromfs.bin: pick the War Thunder install folder`, true);
  }
  await openSource({ kind: "files", files }, label);
}

async function installFiles(root: FileSystemDirectoryEntry) {
  const files = new Map<string, File>();
  const add = async (path: string, entry: FileSystemEntry) => {
    files.set(path, await new Promise<File>((ok, fail) => (entry as FileSystemFileEntry).file(ok, fail)));
  };
  const child = (path: string, dir: boolean) => new Promise<FileSystemEntry | null>((ok, fail) => {
    const done = (err: DOMException) => (err.name === "NotFoundError" ? ok(null) : fail(err));
    if (dir) root.getDirectory(path, {}, ok, done);
    else root.getFile(path, {}, ok, done);
  });
  for (const path of INSTALL_FILES) {
    const entry = await child(path, false);
    if (entry) await add(path, entry);
  }
  const dir = await child(TANKS, true);
  if (dir) {
    const reader = (dir as FileSystemDirectoryEntry).createReader();
    // readEntries gives one batch per call and an empty batch at the end
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((ok, fail) => reader.readEntries(ok, fail));
      if (!batch.length) break;
      await Promise.all(batch.filter((e) => e.isFile && e.name.endsWith(".grp")).map((e) => add(`${TANKS}/${e.name}`, e)));
    }
  }
  return files;
}

async function restore() {
  let handle: FileSystemDirectoryHandle | undefined;
  try {
    handle = await store.folder("get");
  } catch (err) {
    return setStatus(`Cannot read the remembered folder: ${message(err)}`, true);
  }
  if (handle === undefined) return;
  let perm: PermissionState;
  try {
    perm = await handle.queryPermission({ mode: "read" });
    if (perm === "granted") await handle.keys().next(); // throws when the folder is gone
  } catch (err) {
    console.warn(`remembered folder dropped: ${message(err)}`);
    return remember(null);
  }
  saved = handle;
  if (perm === "granted") {
    showSaved(false);
    openSource({ kind: "handle", handle }, handle.name);
    return;
  }
  showSaved(true);
  setStatus("The browser asks for read access again.");
}

async function remember(handle: FileSystemDirectoryHandle | null) {
  saved = handle;
  showSaved(false);
  try {
    await (handle ? store.folder("put", handle) : store.folder("delete"));
  } catch (err) {
    setStatus(`Cannot remember the folder: ${message(err)}`, true);
  }
}

function showSaved(prompt: boolean) {
  $("#open").textContent = saved ? "Choose another" : "Open game folder";
  $("#forget").hidden = !saved;
  $("#reopen").hidden = !prompt;
  if (prompt && saved) $("#reopen").textContent = `Reopen ${saved.name}`;
}

async function openSource(src: Source, label: string) {
  if (managerDown) {
    startManager();
    setWorkers(Number($<HTMLSelectElement>("#workers").value));
    restarts = 0;
    // The new manager loads the shown data first: when src fails, it holds the data of the trees on the page.
    if (shown) void openSource(shown.src, shown.label);
  }
  const open = $<HTMLButtonElement>("#open");
  const id = ++openSeq;
  open.disabled = true;
  setStatus(`Loading ${label}…`);
  const { info, error } = await new Promise<Opened>((resolve) => {
    opens.set(id, resolve);
    tell({ op: "open", id, source: src });
  });
  // The manager runs the opens in order. Only the newest open shows its data, or the held data when it fails.
  if (id !== openSeq) {
    if (info) held = { info, src, label };
    return false;
  }
  open.disabled = false;
  const data = info ? { info, src, label } : held;
  held = null;
  if (data) {
    state.game = new Map(data.info.units.map((u) => [u.id, { ...u, name: clean(u.name) }]));
    trees = data.info.trees;
    render();
    fillPicker();
    shown = { src: data.src, label: data.label };
    const inTree = [...cards.keys()].filter((id) => state.game.has(id)).length;
    const summary = $("#data-summary");
    summary.textContent = `Game data: ${data.label} · ${data.info.source}${data.info.version ? ` ${data.info.version}` : ""} · ${inTree} vehicles`;
    summary.hidden = false;
    field("seatChange").placeholder = String(+data.info.seatChange.toFixed(2));
  }
  folderMode();
  if (!info) {
    setStatus(error!, true);
    return false;
  }
  setStatus("");
  return true; // the manager keeps the shooter that the user picked before, if the new data has it
}

// With game data on the page, the status is in the settings. So an error also shows under the header,
// and the cog gets a mark until the user hides the message.
function setStatus(text: string, error = false) {
  const el = $("#data-status");
  el.textContent = text;
  el.classList.toggle("error", error);
  $("#notice-text").textContent = text;
  showNotice(error && !!shown);
}

function showNotice(on: boolean) {
  $("#notice").hidden = !on;
  $("#cog").classList.toggle("alert", on);
}

// Before the first good open, and while no open has given data, the folder controls stand in the
// middle of the page. Once the page shows game data, they go into the settings.
function folderMode() {
  $(shown ? "#folder-slot" : "#start").append($("#folder"));
  $("#start").hidden = !!shown;
  $("#folder-away").hidden = !!shown;
  $("#open").title = shown ? openTip : "";
  if (!window.showDirectoryPicker) {
    $("#open").textContent = shown ? "Open another folder" : "Open game folder";
    $("#drop-note").hidden = !shown;
  }
}

function name(id: string) {
  return state.game.get(id)?.name ?? id;
}

function nation(country: string) {
  return trees.find((t) => t.country === country)?.name ?? country;
}

function pickerLabel(id: string) {
  return `${name(id)} (${nation(state.game.get(id)!.country)}, ${br(id).toFixed(1)})`;
}

// RB battle rating; NaN when the unit is not in the game data
function br(id: string) {
  return state.game.get(id)?.br ?? NaN;
}

function shootable(id: string) {
  return state.game.has(id);
}

// The search box is a clear pick, so it runs with no question. A new shooter fires the best of all rounds.
function selectShooter(id: string) {
  run({ shooter: id, round: id === state.shooter ? state.round : -1, lines: lines(), opts: model() });
}

// The choice in the confirm bar. In the open settings the bar stands in slot, and the focus goes back to back.
let asking: { c: Choice; slot: string; back: HTMLElement | null } | null = null;

// Runs choice c. When the queue still has work and the store does not have all results of c, the confirm bar
// asks first. The settings show the choice that Run starts: a new question replaces the old one, and its choice
// holds the changes of the old one. Cancel puts the settings back to the shown queue.
async function choose(c: Choice, question: string, slot = "#confirm-slot") {
  const back = document.activeElement instanceof HTMLElement && document.activeElement.closest("#settings") ? document.activeElement : null;
  const busy = () => state.shooter !== "" && state.progress.done < state.progress.total && !state.stopped && pool.length > 0;
  if (!busy()) return run(c);
  let have = 0;
  try {
    have = (await stored(c)).size;
  } catch (err) {
    console.warn(`cannot read the kept results: ${message(err)}`);
  }
  if (!busy() || have >= state.queue.length) return run(c);
  asking = { c, slot, back };
  const { done, total } = state.progress;
  $("#confirm-text").textContent = `${question} The queue of ${name(state.shooter)} stops at ${done} / ${total}; its results stay kept.`
    + (have ? ` ${have} of the new results are kept already.` : "");
  placeConfirm();
  $("#confirm").hidden = false;
  $("#confirm-run").focus();
}

// The open settings make the rest of the page inert, so there the bar stands in the settings.
function placeConfirm() {
  const bar = $("#confirm");
  const inside = $<HTMLDialogElement>("#settings").open;
  (inside ? $(asking?.slot ?? "#confirm-slot") : document.body).append(bar);
  bar.classList.toggle("inline", inside);
  bar.style.top = inside ? "" : `${$("main.trees").getBoundingClientRect().top + 8}px`;
}

function answer(yes: boolean) {
  const a = asking;
  asking = null;
  const bar = $("#confirm");
  // the focus goes back to the control that asked when the bar stands in the settings, as the hidden bar cannot keep it
  if (bar.contains(document.activeElement) && $<HTMLDialogElement>("#settings").open) (a?.back ?? $("#lines")).focus();
  bar.hidden = true;
  if (!a) return;
  yes ? run(a.c) : applied();
}

function setupSettings() {
  const dialog = $<HTMLDialogElement>("#settings");
  const cog = $("#cog");
  const open = () => {
    if (!dialog.open) dialog.showModal();
    if (!asking) return;
    placeConfirm();
    $("#confirm-run").focus();
  };
  cog.addEventListener("click", open);
  $("#notice-open").addEventListener("click", open);
  $("#notice-close").addEventListener("click", () => showNotice(false));
  $("#settings-close").addEventListener("click", () => dialog.close());
  // the dialog has no padding, so a click on the dialog itself is a click on the backdrop
  dialog.addEventListener("click", (e) => {
    const r = dialog.getBoundingClientRect();
    if (e.target === dialog && (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom)) dialog.close();
  });
  dialog.addEventListener("close", () => {
    if (asking) placeConfirm();
    cog.focus();
  });
}

function setupConfirm() {
  $("#confirm-run").addEventListener("click", () => answer(true));
  $("#confirm-cancel").addEventListener("click", () => answer(false));
  addEventListener("keydown", (e) => {
    if (!asking) return;
    if (e.key === "Escape") {
      e.preventDefault(); // the settings stay open
      answer(false);
    }
    // Enter on a focused button clicks it already
    else if (e.key === "Enter" && !(e.target instanceof HTMLButtonElement || e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement)) answer(true);
  });
}

let prev: Choice | null = null; // the choice before the last change of shooter

function shooterInfo(note = "") {
  const s = state.game.get(state.shooter)!;
  const info = $("#shooter-info");
  info.textContent = `${s.role}${s.rank ? ` · Rank ${RANKS[s.rank - 1]}` : ""} · BR ${s.br.toFixed(1)} · ${note || "click any card to shoot with it"}`;
  const back = prev;
  if (!back || back.shooter === state.shooter || !state.game.has(back.shooter)) return;
  const button = document.createElement("button");
  button.className = "link-btn";
  button.textContent = `back to ${name(back.shooter)}`;
  button.title = "Shoot with the shooter before. Its kept results show at once.";
  button.addEventListener("click", () => run({ ...back, lines: lines(), opts: model() }));
  info.append(" · ", button);
}

// A new queue of the manager: outwards from the shooter's BR, or none when no shooter is picked.
function newQueue(q: Extract<FromManager, { op: "queue" }>) {
  answer(false); // the question is about the old queue
  const moved = q.shooter !== state.shooter;
  if (state.shooter && q.shooter !== state.shooter) prev = { shooter: state.shooter, round: state.round, lines: state.lines, opts: state.opts! };
  state.gen = q.gen;
  state.shooter = q.shooter;
  state.round = q.round ?? -1;
  state.lines = q.lines;
  $<HTMLSelectElement>("#lines").value = String(q.lines);
  state.opts = q.options;
  fillModel(q.options);
  legend(q.options);
  state.rounds = q.rounds ? { range: q.range!, rounds: q.rounds } : null;
  state.queue = q.targets ?? [];
  if (q.source !== undefined) state.data = { source: q.source, version: q.version ?? "" };
  state.run = state.queue.length ? runKey(state as Choice) : "";
  state.started.clear();
  state.results.clear();
  state.kept.clear();
  state.progress = { done: 0, total: state.queue.length, running: 0, paused: false };
  state.stopped = "";
  pinned = false;
  for (const els of cards.values()) {
    els.forEach((el) => {
      el.classList.remove("running", "done", "void");
      el.style.removeProperty("--bg");
      el.querySelectorAll<HTMLElement>(".card-views i").forEach((i) => i.style.removeProperty("background"));
      el.querySelector<HTMLElement>(".card-gain")!.hidden = true;
    });
  }
  document.querySelectorAll(".card.shooter").forEach((el) => el.classList.remove("shooter"));
  const input = $<HTMLInputElement>("#shooter");
  if (!state.shooter) {
    input.value = "";
    $("#shooter-info").textContent = "type a name or click any card to shoot with it";
    $("#trees").classList.add("idle");
  } else {
    input.value = pickerLabel(state.shooter);
    // a long name makes the box wider, so that the name shows in full
    input.style.width = "";
    if (input.scrollWidth > input.clientWidth) input.style.width = `min(${input.offsetWidth + input.scrollWidth - input.clientWidth + 4}px, calc(100vw - 140px))`;
    shooterInfo(state.rounds && !state.rounds.rounds.length ? "no gun of 20 mm or more that attacks armour" : "");
    cards.get(state.shooter)?.forEach((el) => el.classList.add("shooter"));
    $("#trees").classList.remove("idle");
  }
  shooterNation();
  markRuler();
  if (moved) centerShooter();
  roundsPanel();
  if (q.error) $("#rounds").textContent = `Cannot read the rounds: ${q.error}`;
  progress();
  if (state.queue.length) void fromStore(q.gen);
}

// The key of the results of a choice in the store: all that changes a result.
function runKey(c: Choice) {
  return JSON.stringify([BUILD_ID, state.data.source, state.data.version, c.shooter, c.round, c.lines, c.opts]);
}

// The results of choice c that the store has. A forced round also takes the targets of the run of all rounds,
// whose results hold each round. rounds gives the shell of such a result; without it, the shell is null.
async function stored(c: Choice, rounds?: Shell[]) {
  const out = new Map<string, SimResult>();
  for (const [target, s] of await store.results(runKey(c))) out.set(target, JSON.parse(s));
  if (c.round < 0) return out;
  for (const [target, s] of await store.results(runKey({ ...c, round: -1 }))) {
    if (out.has(target)) continue;
    const all: SimResult = JSON.parse(s);
    const r = all.each?.[c.round];
    if (!r || all.each!.length !== (rounds?.length ?? all.each!.length)) continue;
    out.set(target, { shooter: all.shooter, target, shell: rounds?.[c.round] ?? null, views: r.views, score: r.score, ms: r.ms, shots: r.shots, extra: r.extra });
  }
  return out;
}

// Shows the results of queue gen that the store has, and starts the queue for the other targets.
async function fromStore(gen: number) {
  let have = new Map<string, SimResult>();
  try {
    await store.prune(state.run);
    have = await stored(state as Choice, state.rounds?.rounds);
  } catch (err) {
    console.warn(`cannot read the kept results: ${message(err)}`);
  }
  if (gen !== state.gen) return;
  for (const [id, r] of have) {
    delete r.each;
    state.results.set(id, r);
    state.kept.add(id);
    paint(id);
  }
  tell({ op: "go", gen, done: [...have.keys()] });
}

// Once the queue is done, the tooltip of the hover parameter's unit stays on.
function pinHover() {
  pinned = true;
  const pin = params.get("hover");
  const els = pin ? cards.get(pin) : undefined;
  if (!els) return;
  const u = state.game.get(pin!);
  if (state.view === "nation" && u) showNation(u.country, false);
  els[0].scrollIntoView({ block: "center", inline: "center" });
  showTooltip(els[0], true);
}

// The shooter's rounds at the range: click one to fire only it, click it again for the best of all.
function roundsPanel() {
  const panel = $("#rounds");
  const r = state.rounds;
  roundsWidth = 0;
  $(".bar").classList.remove("under");
  panel.classList.toggle("forced", state.round >= 0);
  if (!state.shooter) {
    panel.innerHTML = `<div class="rounds-head">Pick a shooter: its gun and rounds show here</div>`;
    return;
  }
  if (!r) return panel.replaceChildren();
  if (!r.rounds.length) {
    panel.innerHTML = `<div class="rounds-head">No gun of 20 mm or more that attacks armour</div>`;
    return;
  }
  const head = state.round >= 0
    ? `only <b>${esc(r.rounds[state.round].name)}</b> · ${r.range} m · ${state.lines} lines <span class="hint">· click it again for the best of all</span>`
    : `best of ${r.rounds.length} round${r.rounds.length === 1 ? "" : "s"} per target · ${r.range} m · ${state.lines} lines a round <span class="hint">· click a round to fire only it</span>`;
  const guns = [...new Set(r.rounds.map((s) => s.gun))].map((gun) => r.rounds.filter((s) => s.gun === gun));
  const tables = (most: number) => guns.map((list) => {
    const size = Math.ceil(list.length / Math.ceil(list.length / most));
    const out: string[] = [];
    for (let k = 0; k < list.length; k += size) {
      const rows = list.slice(k, k + size).map((s) => {
        const filler = s.filler ? `${+(s.filler * 1000).toFixed(1)} <span class="muted">${esc(s.explosive.replaceAll("_", " "))}</span>` : "";
        return `<tr class="round${s.index === state.round ? " forced" : ""}" data-index="${s.index}"><td>${esc(s.name)}</td>`
          + `<td>${shellType(s.type)}</td><td>${+s.caliber.toFixed(1)}</td><td>${s.mass.toFixed(s.mass >= 1 ? 2 : 3)}</td><td>${s.speed}</td>`
          + `<td>${Math.round(s.pen)}</td><td>${filler}</td><td class="r-wins">0</td></tr>`;
      }).join("");
      out.push(`<table><caption>${esc(list[0].gun)}</caption>`
        + `<tr><th></th><th>type</th><th>mm</th><th>kg</th><th title="muzzle speed">m/s</th><th title="flat penetration at ${r.range} m">pen mm</th><th title="filler mass and explosive">filler g</th><th title="targets this round was best on">best</th></tr>`
        + rows + `</table>`);
    }
    return out.join("");
  }).join("");
  // Each gun's rounds go in tables of at most a number of rows, side by side. The rounds stand right of the
  // search section or under it. The page tries each layout and keeps the lowest header, so that no round is
  // hidden. On a tie, it keeps the rounds at the right, and then more rows.
  const bar = panel.parentElement!;
  const most = Math.max(...guns.map((l) => l.length));
  const show = (under: boolean, n: number) => {
    bar.classList.toggle("under", under);
    panel.innerHTML = `<div class="rounds-head">${head}</div><div class="rounds-list">${tables(n)}</div>`;
    return bar.offsetHeight;
  };
  let best = { under: false, n: 0 }, low = Infinity;
  for (const under of [false, true]) {
    for (let n = Math.min(3, most); n <= most; n++) {
      const h = show(under, n);
      if (h < low || (h === low && under === best.under)) [best, low] = [{ under, n }, h];
    }
  }
  show(best.under, best.n);
  roundsWidth = panel.parentElement!.clientWidth;
}
let roundsWidth = 0; // of the header at the last layout of the rounds

function highlightRound(index: number) {
  document.querySelectorAll<HTMLElement>("#rounds .round").forEach((el) => el.classList.toggle("hl", Number(el.dataset.index) === index));
}

function hideTip() {
  $("#tooltip").hidden = true;
  highlightRound(-1);
}

function progress() {
  const { done, total, running, paused, at } = state.progress;
  // the wins of each round, also on the kept results, and the time of the results of this queue
  const wins: number[] = [];
  let ms = 0, n = 0;
  for (const [id, r] of state.results) {
    if ("error" in r || !r.shell) continue;
    wins[r.shell.index] = (wins[r.shell.index] ?? 0) + 1;
    if (state.kept.has(id)) continue;
    ms += r.ms;
    n++;
  }
  document.querySelectorAll<HTMLElement>("#rounds .round").forEach((el) => (el.querySelector(".r-wins")!.textContent = String(wins[Number(el.dataset.index)] ?? 0)));
  const stopped = state.stopped || (!pool.length && done < total ? "all compute workers failed. Change the number of workers to start new ones." : "");
  const pause = $<HTMLButtonElement>("#pause");
  pause.disabled = done >= total || !!stopped;
  pause.textContent = paused ? "Resume" : "Pause";
  $("#progress-fill").style.width = `${total ? (100 * done) / total : 0}%`;
  const text = $("#progress-text");
  if (stopped) {
    text.textContent = `Stopped: ${stopped}`;
    return;
  }
  if (!total) {
    text.textContent = "Pick a shooter to run the shots";
    return;
  }
  const avg = n ? ` · ${Math.round(ms / n)} ms a pair` : "";
  const where = paused ? ` · paused${running ? `, ${running} still running` : ""}` : at !== undefined ? ` · now at BR ${at.toFixed(1)}` : "";
  const kept = state.kept.size ? ` · ${state.kept.size} kept` : "";
  text.textContent = done < total ? `${done} / ${total}${kept}${where}${avg}` : `${total} / ${total} done${kept}${avg}`;
  text.title = state.kept.size ? `${state.kept.size} results came from the results kept in this browser (valid for one day), the others ran now` : "";
}

function paint(id: string) {
  const res = state.results.get(id);
  if (!res) return;
  for (const el of cards.get(id) ?? []) {
    el.classList.remove("running");
    el.classList.add("done");
    if ("error" in res || !res.shell) {
      el.classList.add("void");
      continue;
    }
    el.style.setProperty("--bg", color(res.score));
    el.querySelectorAll<HTMLElement>(".card-views i").forEach((i, v) => (i.style.background = color(res.views[v]?.score ?? 0, 46)));
    const bar = el.querySelector<HTMLElement>(".card-gain")!;
    bar.hidden = !res.shell.followUps || !state.opts?.followUps && !state.opts?.pairs;
    if (bar.hidden) continue;
    const [first, follow, pairs] = cardGains(res.views).map((g) => g * 100);
    const [run, dots, tick] = bar.children as HTMLCollectionOf<HTMLElement>;
    run.style.cssText = `left:${first}%;width:${follow - first}%;background:${color(follow / 100, 60)}`;
    dots.style.cssText = `left:${follow}%;width:${pairs - follow}%;color:${color(pairs / 100, 62)}`;
    tick.style.left = `${first}%`;
  }
}

function color(score: number, light = 30) {
  return `hsl(${Math.round(score * 125)} 58% ${light}%)`;
}

function shellType(t: string) {
  const types = [["apds_fs", "APFSDS"], ["apds", "APDS"], ["apcr", "APCR"], ["apcbc", "APCBC"], ["aphebc", "APHEBC"], ["aphe", "APHE"],
    ["apbc", "APBC"], ["apc", "APC"], ["heat_fs", "HEAT-FS"], ["heat", "HEAT"], ["hesh", "HESH"], ["atgm", "ATGM"], ["sap", "SAP"], ["ap", "AP"], ["he", "HE"]];
  return types.find(([p]) => t.startsWith(p))?.[1] ?? t;
}

function plateText(p: Plate) {
  return `${esc(p.name.replaceAll("_", " "))} <b>${Math.round(p.mm)}</b><span class="of">×${p.shots}</span>`;
}

// The score of view v: of the first shot (as the simulation gives it), with the follow-up kills, and with the pair kills
// too. See README.md, "Colours".
function gains(v: View) {
  const n = OUTCOMES.reduce((a, o) => a + (v.counts[o] ?? 0), 0);
  const f: Follow = v.follow ?? {};
  const kills = f.kills ?? 0;
  const pairs = f.pairs ? (f.pairKills ?? 0) / f.pairs * ((f.lines ?? 0) - kills) : 0;
  return n ? [v.score, v.score + 0.6 * kills / n, v.score + 0.6 * (kills + pairs) / n] : [v.score, v.score, v.score];
}

// The card score of each of the three gains.
function cardGains(views: View[]) {
  const out = [0, 0, 0];
  views.forEach((v, i) => gains(v).forEach((g, k) => (out[k] += VIEW_WEIGHT[i] * g)));
  return out;
}

function frac(a = 0, b = 0) {
  if (!b) return `<span class="z">–</span>`;
  return `<span${a ? "" : ` class="z"`}>${a}<span class="of">/${b}</span></span>`;
}

function num(n = 0) {
  return n ? String(n) : `<span class="z">·</span>`;
}

const pct = (s: number) => `${Math.round(s * 100)}%`;

function resultBody(res: SimResult, sh: Shell, kept: boolean) {
  const n = state.rounds?.rounds.length ?? 0;
  const guided = !sh.followUps;
  const o = state.opts!;
  const follow = !guided && o.followUps, pairs = !guided && o.pairs;
  const views = res.views;
  const fl = views.map((v): Follow => v.follow ?? {});
  const row = (label: string, cell: (i: number) => string, cls = "") =>
    `<tr${cls ? ` class="${cls}"` : ""}><td>${label}</td>${views.map((_, i) => `<td>${cell(i)}</td>`).join("")}</tr>`;
  const any = (k: keyof Follow) => fl.some((f) => f[k]);
  const g = views.map(gains);
  let rows = `<tr><th></th>${views.map((v) => `<th>${v.name}</th>`).join("")}</tr>`
    + `<tr class="tt-bars"><td></td>${views.map((v) => `<td><span class="tt-bar">${OUTCOMES.map((o) => v.counts[o] ? `<i class="o-${o}" style="flex:${v.counts[o]}"></i>` : "").join("")}</span></td>`).join("")}</tr>`
    + row("First shot", (i) => `<i class="mk"><i class="tick"></i></i>${pct(g[i][0])}`, "tt-first");
  if (follow) rows += row("+ follow-ups", (i) => `<i class="mk"><i class="run"></i></i>${pct(g[i][1])}`);
  if (pairs) rows += row("+ pairs", (i) => `<i class="mk"><i class="dots"></i></i>${pct(g[i][2])}`);
  for (const o of OUTCOMES) {
    if (views.some((v) => v.counts[o])) rows += row(`<i class="sw o-${o}"></i>${OUTCOME_LABEL[o]}`, (i) => num(views[i].counts[o]));
  }
  rows += row("Lines", (i) => String(OUTCOMES.reduce((a, o) => a + (views[i].counts[o] ?? 0), 0)), "tt-total");
  if (follow || pairs || any("silenced")) {
    rows += `<tr class="tt-sec"><td colspan="${views.length + 1}">Extra shots <span class="muted">${guided ? "none: guided" : follow ? `${sh.followUps} in ${sh.followSec} s` : ""}</span></td></tr>`;
  }
  if (follow && any("lines")) {
    const mean = (i: number, k: "shots" | "seconds", d: number) => fl[i].kills ? (fl[i][k]! / fl[i].kills!).toFixed(d) : `<span class="z">–</span>`;
    rows += row("Follow-up", (i) => frac(fl[i].kills, fl[i].lines))
      + row(`<span class="ind">shots to kill</span>`, (i) => mean(i, "shots", 1))
      + row(`<span class="ind">time to kill</span>`, (i) => mean(i, "seconds", 2));
  }
  if (any("pairs")) rows += row("Pair kill", (i) => frac(fl[i].pairKills, fl[i].pairs));
  if (any("silenced")) {
    rows += row(`<span class="warn">Gun out</span>`, (i) => num(fl[i].silenced));
    if (!guided) {
      rows += row(`<span class="ind">3rd shot ${sh.thirdSec} s</span>`, (i) => frac(fl[i].third, fl[i].silenced))
        + row(`<span class="ind">kill in 3</span>`, (i) => frac(fl[i].thirdKills, fl[i].third));
    }
  }
  const c = cardGains(views);
  return `<div class="tt-shot">${esc(sh.name)} <span class="muted">${shellType(sh.type)}</span> <b>${sh.pen}</b><span class="muted"> mm at ${state.rounds?.range ?? 500} m</span></div>`
    + `<div class="tt-meta">${esc(sh.gun)} · ${state.round >= 0 ? "only this round" : `best of ${n}`} · ${state.lines} lines${guided ? " · guided" : follow ? ` · ${sh.followUps} follow-up${sh.followUps === 1 ? "" : "s"} in ${sh.followSec} s` : ""}</div>`
    + `<table class="tt-t">${rows}</table>`
    + `<div class="tt-sec">Plates</div>`
    + views.map((v) => `<div class="tt-pl"><span>${v.name}</span><span>${v.plates.slice(0, 2).map(plateText).join(" · ")}</span></div>`).join("")
    + `<div class="tt-foot"><span>Lethality <b>${pct(res.score)}</b>${follow || pairs ? ` <span class="muted">${follow ? `→ ${pct(c[1])} ` : ""}${pairs ? `→ ${pct(c[2])}` : ""}</span>` : ""}</span>`
    + `<span class="muted">${res.shots}${res.extra ? ` + ${res.extra}` : ""} shots · ${kept ? "kept" : `${Math.round(res.ms)} ms`}</span></div>`;
}

function showTooltip(el: HTMLElement, pinned = false) {
  const tip = $("#tooltip");
  const id = el.dataset.id!;
  const u = state.game.get(id);
  const res = state.results.get(id);
  let body;
  if (!u) {
    body = `<div class="tt-pending">Not in the opened game data, not simulated</div>`;
  } else if (!state.shooter) {
    body = `<div class="tt-pending">No shooter picked. Click the card to shoot with it.</div>`;
  } else if (state.started.has(id)) {
    body = `<div class="tt-pending">Running now</div>`;
  } else if (!res) {
    const at = state.queue.indexOf(id);
    const ahead = state.queue.slice(0, Math.max(0, at)).filter((t) => !state.started.has(t) && !state.results.has(t)).length;
    body = `<div class="tt-pending">Queued: ${ahead} ahead in line${state.progress.paused ? " (paused)" : ""}</div>`;
  } else if ("error" in res) {
    body = `<div class="tt-pending">Failed: ${esc(res.error)}</div>`;
  } else if (!res.shell) {
    body = `<div class="tt-pending">The shooter has no gun of 20 mm or more that attacks armour</div>`;
  } else {
    body = resultBody(res, res.shell, state.kept.has(id));
  }
  const sbr = br(state.shooter);
  const d = shootable(id) && state.shooter ? br(id) - sbr : 0;
  const diff = state.shooter ? ` <span class="${d > 0 ? "up" : d < 0 ? "down" : ""}">(${d >= 0 ? "+" : ""}${d.toFixed(1)})</span>` : "";
  tip.innerHTML = `<div class="tt-head"><span class="tt-title">${esc(name(id))}</span>${shootable(id) ? `<span class="tt-br">BR ${br(id).toFixed(1)}${diff}</span>` : ""}</div>`
    + `<div class="tt-sub">${u ? `${esc(nation(u.country))} · ${esc(u.role)}${u.rank ? ` · Rank ${RANKS[u.rank - 1]}` : ""}` : ""}</div>`
    + (state.shooter ? `<div class="tt-shooter">${esc(name(state.shooter))}${id === state.shooter ? " (itself)" : ""}</div>` : "")
    + body;
  tip.hidden = false;
  highlightRound(res && !("error" in res) && res.shell ? res.shell.index : -1);
  const rect = el.getBoundingClientRect();
  const w = tip.offsetWidth, h = tip.offsetHeight;
  let x = rect.right + 8, y = rect.top;
  if (x + w > innerWidth - 8) x = rect.left - w - 8;
  if (y + h > innerHeight - 8) y = innerHeight - h - 8;
  tip.style.left = `${Math.max(8, x)}px`;
  tip.style.top = `${Math.max(8, y)}px`;
  tip.classList.toggle("pinned", pinned);
}

function syncUrl() {
  const p = new URLSearchParams(location.search);
  p.delete("shooter"); // from older links: the page does not pick a shooter by itself
  p.delete("mode");
  !state.nations || state.nations.size === trees.length ? p.delete("nations") : p.set("nations", [...state.nations].join(","));
  state.view === "vertical" ? p.delete("view") : p.set("view", state.view);
  state.view === "nation" ? p.set("nation", state.nation) : p.delete("nation");
  history.replaceState(null, "", `?${p}`);
}

function esc(s: string) {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
}
