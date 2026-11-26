// Tech trees and names from the opened game data, unit icons from the wiki's static content; shot results from the Go simulation.
// The manager worker (manager.wasm) reads the game files and runs the queue. It hands the targets to a pool of compute
// workers (compute.wasm) over MessagePorts. The page only draws and passes the user's actions to the manager.
import type { Card, ComputeFatal, FromManager, GameInfo, Grid, Outcome, Plate, Progress, Rounds, Source, TargetResult, ToManager, Tree, Unit } from "./types";

const RANKS = ["I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X"];
const OUTCOMES: Outcome[] = ["kill", "pen", "shatter", "ricochet", "nonpen"];
const OUTCOME_LABEL: Record<Outcome, string> = { kill: "Pen + kill", pen: "Pen, no kill", shatter: "Pen, core broke", ricochet: "Ricochet", nonpen: "No pen" };
const TILE_MIN = 8, TILE_MAX = 70; // TILE_MAX: the height of the unit pictures and the view bars

const params = new URLSearchParams(location.search);
const nationsParam = params.get("nations");
const state = {
  shooter: "", // none until the user picks one
  nations: nationsParam ? new Set(nationsParam.split(",")) : null as Set<string> | null, // null: all
  game: new Map<string, Unit>(), // from the opened game data
  results: new Map<string, TargetResult>(),
  rounds: null as Rounds | null, // of the shooter
  round: -1, // the only round to fire, or -1 for the best of all
  lines: 0, // shot lines a round on each target, in the shown queue
  queue: [] as string[], // in the manager's order
  started: new Set<string>(), // targets that a compute worker holds
  gen: 0, // the manager's queue; messages of other queues are dropped
  progress: { done: 0, total: 0, running: 0, ms: 0, paused: false, wins: null } as Progress,
  stopped: "", // why the queue stopped, when the manager failed
  byLabel: new Map<string, string>(),
};

const $ = <T extends HTMLElement = HTMLElement>(s: string) => document.querySelector<T>(s)!;
// WT font glyphs (origin and event markers) that a normal font cannot draw
const clean = (name: string) => name.replace(/[␀-◿-]/g, "").trim();
const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

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
  // the manager keeps its default for a count that it does not have
  tell({ op: "lines", n: Number(localStorage.getItem("shotdb.lines")) });
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
setupCompact();
setupPicker();
setupOpen();

// Default: half of the cores, at most 8.
function setupWorkers() {
  const select = $<HTMLSelectElement>("#workers");
  for (let n = 1; n <= cores; n++) select.add(new Option(String(n)));
  const stored = Number(localStorage.getItem("shotdb.workers"));
  select.value = String(stored >= 1 && stored <= cores ? stored : Math.min(8, Math.max(1, Math.floor(cores / 2))));
  select.addEventListener("change", () => {
    localStorage.setItem("shotdb.workers", select.value);
    restarts = 0;
    setWorkers(Number(select.value));
  });
  setWorkers(Number(select.value));
}

// The manager gives the counts in its first message, "samples". A change starts the queue of the shooter again.
function setupLines() {
  const select = $<HTMLSelectElement>("#lines");
  select.addEventListener("change", () => {
    localStorage.setItem("shotdb.lines", select.value);
    tell({ op: "lines", n: Number(select.value) });
  });
}

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
      if ("result" in msg) restarts = 0;
      state.started.delete(msg.target);
      state.results.set(msg.target, "result" in msg ? msg.result : { target: msg.target, error: msg.error });
      paint(msg.target);
      break;
    case "progress": {
      const { op: _op, gen: _gen, ...p } = msg;
      state.progress = p;
      document.querySelectorAll<HTMLElement>("#rounds .r-wins").forEach((el, i) => (el.textContent = String(p.wins?.[i] ?? 0)));
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
    if (card && shootable(card.dataset.id!)) selectShooter(card.dataset.id!);
  });
  main.addEventListener("mouseover", (e) => {
    const card = (e.target as Element).closest<HTMLElement>("[data-id]");
    if (card) showTooltip(card);
  });
  main.addEventListener("mouseleave", hideTip);
  main.parentElement!.addEventListener("scroll", () => {
    if (!tip.classList.contains("pinned")) hideTip();
  }, { passive: true });
  main.addEventListener("mouseout", (e) => {
    if (!(e.relatedTarget instanceof Element && e.relatedTarget.closest("[data-id]"))) hideTip();
  });
  $("#rounds").addEventListener("click", (e) => {
    const row = (e.target as Element).closest<HTMLElement>(".round");
    if (!row) return;
    tell({ op: "round", index: Number(row.dataset.index) });
  });
  $("#pause").addEventListener("click", () => tell({ op: "pause" }));
  main.innerHTML = `<p class="empty">Open the War Thunder install folder to show the research trees.</p>`;
  $("#progress-text").textContent = "Open a game folder to run the shots";
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
      state.nations ??= new Set(trees.map((t) => t.country));
      state.nations.has(tree.country) ? state.nations.delete(tree.country) : state.nations.add(tree.country);
      applyNations();
    });
    nav.append(chip);

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
  // The queue can be older than the trees: the held data of an older open shows after the queue of that open started.
  cards.get(state.shooter)?.forEach((el) => el.classList.add("shooter"));
  for (const id of state.started) cards.get(id)?.forEach((el) => el.classList.add("running"));
  for (const id of state.results.keys()) paint(id);
  applyNations();
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
    + `<span class="card-views"><i></i><i></i><i></i></span>`;
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
  let col = 0;
  for (const tree of trees) {
    const on = !state.nations || state.nations.has(tree.country);
    if (on) col++;
    document.querySelectorAll<HTMLElement>(`.n-${tree.country}`).forEach((el) => {
      el.hidden = !on;
      el.style.gridColumn = String(col);
    });
    document.querySelector(`.chip[data-country="${tree.country}"]`)!.classList.toggle("on", on);
  }
  syncUrl();
  fitCompact();
}


function setupCompact() {
  const button = $("#compact");
  const set = (on: boolean) => {
    document.body.classList.toggle("compact", on);
    button.classList.toggle("on", on);
    fitCompact();
  };
  button.addEventListener("click", () => {
    const on = !document.body.classList.contains("compact");
    localStorage.setItem("shotdb.compact", on ? "1" : "0");
    set(on);
  });
  set(localStorage.getItem("shotdb.compact") === "1");
  // the view changes size with the window and with the header (the rounds panel)
  new ResizeObserver(() => fitCompact()).observe($("#trees").parentElement!);
}

// The compact grid is only as wide as the shown trees, and its width is linear in the tile
// size. Two trial layouts give the line, which gives the largest tile that fits the view's width.
function fitCompact() {
  const main = $("#trees");
  const note = $("#compact-note");
  if (!document.body.classList.contains("compact") || !main.querySelector(".nation-head:not([hidden])")) {
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

function setupOpen() {
  if (!window.showDirectoryPicker) {
    // Firefox: a dropped folder or a folder input. Both give File objects that the page reads in the browser.
    // The input exists only here, so that browsers with the File System Access API never use it.
    const input = document.createElement("input");
    input.type = "file";
    input.webkitdirectory = true;
    input.hidden = true;
    document.body.append(input);
    const open = $("#open");
    open.title = "The War Thunder install folder (with aces.vromfs.bin). The browser can call this an upload, "
      + "but the page reads the files on this computer and sends nothing.";
    open.addEventListener("click", () => input.click());
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
    $("#trees").innerHTML = `<p class="empty">Press <b>Open game folder</b> or drop the War Thunder install folder on this page to show the research trees.<br>`
      + `The browser can call this an upload, but the files stay on this computer: the page reads them here and sends nothing.<br>`
      + `This browser cannot remember the folder, so open it again on each visit.</p>`;
    return setStatus("Open or drop the War Thunder install folder. This browser cannot remember the folder.");
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
  $("#forget").addEventListener("click", () => remember(null));
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
    handle = await handleStore("get");
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
  setStatus(`The browser asks again for read access to ${handle.name}`);
}

async function remember(handle: FileSystemDirectoryHandle | null) {
  saved = handle;
  showSaved(false);
  try {
    await (handle ? handleStore("put", handle) : handleStore("delete"));
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

// One FileSystemDirectoryHandle in IndexedDB.
function handleStore(op: "get" | "put" | "delete", handle?: FileSystemDirectoryHandle) {
  return new Promise<FileSystemDirectoryHandle | undefined>((resolve, reject) => {
    const req = indexedDB.open("shotdb", 1);
    req.onupgradeneeded = () => req.result.createObjectStore("folder");
    req.onerror = () => reject(req.error);
    req.onsuccess = () => {
      const db = req.result;
      const tx = db.transaction("folder", op === "get" ? "readonly" : "readwrite");
      const store = tx.objectStore("folder");
      const r: IDBRequest = op === "get" ? store.get("game") : op === "put" ? store.put(handle, "game") : store.delete("game");
      tx.oncomplete = () => {
        db.close();
        resolve(r.result);
      };
      tx.onerror = () => {
        db.close();
        reject(tx.error);
      };
    };
  });
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
  }
  if (!info) {
    setStatus(error!, true);
    return false;
  }
  const inTree = [...cards.keys()].filter((id) => state.game.has(id)).length;
  setStatus(`${label} · ${info.source}${info.version ? ` ${info.version}` : ""} · ${inTree} vehicles`);
  return true; // the manager keeps the shooter that the user picked before, if the new data has it
}

function setStatus(text: string, error = false) {
  const el = $("#data-status");
  el.textContent = text;
  el.title = text;
  el.classList.toggle("error", error);
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

function selectShooter(id: string) {
  tell({ op: "shooter", id });
}

function shooterInfo(note = "") {
  const s = state.game.get(state.shooter)!;
  $("#shooter-info").textContent = `${s.role}${s.rank ? ` · Rank ${RANKS[s.rank - 1]}` : ""} · BR ${s.br.toFixed(1)} · ${note || "click any card to shoot with it"}`;
}

// A new queue of the manager: outwards from the shooter's BR, or none when no shooter is picked.
function newQueue(q: Extract<FromManager, { op: "queue" }>) {
  state.gen = q.gen;
  state.shooter = q.shooter;
  state.round = q.round ?? -1;
  state.lines = q.lines;
  $<HTMLSelectElement>("#lines").value = String(q.lines);
  state.rounds = q.rounds ? { range: q.range!, rounds: q.rounds } : null;
  state.queue = q.targets ?? [];
  state.started.clear();
  state.results.clear();
  state.progress = { done: 0, total: state.queue.length, running: 0, ms: 0, paused: false, wins: null };
  state.stopped = "";
  pinned = false;
  for (const els of cards.values()) {
    els.forEach((el) => {
      el.classList.remove("running", "done", "void");
      el.style.removeProperty("--bg");
      el.querySelectorAll<HTMLElement>(".card-views i").forEach((i) => i.style.removeProperty("background"));
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
    shooterInfo(state.rounds && !state.rounds.rounds.length ? "no gun of 20 mm or more that attacks armour" : "");
    cards.get(state.shooter)?.forEach((el) => el.classList.add("shooter"));
    $("#trees").classList.remove("idle");
  }
  roundsPanel();
  if (q.error) $("#rounds").textContent = `Cannot read the rounds: ${q.error}`;
  progress();
}

// Once the queue is done, the tooltip of the hover parameter's unit stays on.
function pinHover() {
  pinned = true;
  const pin = params.get("hover");
  const els = pin ? cards.get(pin) : undefined;
  if (!els) return;
  els[0].scrollIntoView({ block: "center", inline: "center" });
  showTooltip(els[0], true);
}

// The shooter's rounds at the range: click one to fire only it, click it again for the best of all.
function roundsPanel() {
  const panel = $("#rounds");
  const r = state.rounds;
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
  // with one gun its name goes in the head, else above its rounds
  const guns = new Set(r.rounds.map((s) => s.gun));
  let gun = guns.size === 1 ? r.rounds[0].gun : "";
  const head = (gun ? `<b>${esc(gun)}</b>: ` : "") + (state.round >= 0
    ? `only <b>${esc(r.rounds[state.round].name)}</b> at ${r.range} m, ${state.lines} lines · click it again for the best of all`
    : `${r.rounds.length} rounds at ${r.range} m, ${state.lines} lines a round, the best per target · click a round to fire only it`);
  const rows = r.rounds.map((s, i) => {
    const g = s.gun === gun ? "" : `<tr><th class="gun" colspan="8">${esc((gun = s.gun))}</th></tr>`;
    const mass = s.mass >= 1 ? `${s.mass.toFixed(2)} kg` : `${Math.round(s.mass * 1000)} g`;
    const filler = s.filler ? `${+(s.filler * 1000).toFixed(1)} g ${esc(s.explosive.replaceAll("_", " "))}` : "";
    return g + `<tr class="round${i === state.round ? " forced" : ""}" data-index="${i}"><td title="${esc(s.name)}">${esc(s.name)}</td>`
      + `<td>${shellType(s.type)}</td><td>${+s.caliber.toFixed(1)} mm</td><td>${mass}</td><td>${s.speed} m/s</td>`
      + `<td>${Math.round(s.pen)} mm</td><td>${filler}</td><td class="r-wins">${state.progress.wins?.[i] ?? 0}</td></tr>`;
  }).join("");
  panel.innerHTML = `<div class="rounds-head">${head}</div><div class="rounds-list"><table>`
    + `<tr><th></th><th>type</th><th>calibre</th><th>mass</th><th>muzzle</th><th title="flat penetration at ${r.range} m">pen</th><th>filler</th><th title="targets this round was best on">best</th></tr>`
    + rows + `</table></div>`;
}

function highlightRound(index: number) {
  document.querySelectorAll<HTMLElement>("#rounds .round").forEach((el) => el.classList.toggle("hl", Number(el.dataset.index) === index));
}

function hideTip() {
  $("#tooltip").hidden = true;
  highlightRound(-1);
}

function progress() {
  const { done, total, running, ms, paused, at } = state.progress;
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
  const avg = done ? ` · ${Math.round(ms / done)} ms a pair` : "";
  const where = paused ? ` · paused${running ? `, ${running} still running` : ""}` : at !== undefined ? ` · now at BR ${at.toFixed(1)}` : "";
  text.textContent = done < total ? `${done} / ${total}${where}${avg}` : `${total} / ${total} done${avg}`;
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
  return `${esc(p.name.replaceAll("_", " "))} ${Math.round(p.mm)} mm <span class="muted">(${p.shots})</span>`;
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
    const n = state.rounds?.rounds.length ?? 0;
    body = `<div class="tt-shell">${esc(res.shell.name)} <span class="muted">${shellType(res.shell.type)} · ${res.shell.pen} mm flat at ${state.rounds?.range ?? 500} m</span></div>`
      + `<div class="tt-sub">${esc(res.shell.gun)} · ${state.round >= 0 ? "the only round fired" : `best of ${n} round${n === 1 ? "" : "s"}`} · ${state.lines} lines a round</div>`
      + res.views.map((v) => {
        const total = OUTCOMES.reduce((a, o) => a + (v.counts[o] ?? 0), 0);
        const bar = OUTCOMES.map((o) => v.counts[o] ? `<i class="o-${o}" style="flex:${v.counts[o]}"></i>` : "").join("");
        const parts = OUTCOMES.filter((o) => v.counts[o]).map((o) => `<span class="c-${o}">${v.counts[o]} ${OUTCOME_LABEL[o].toLowerCase()}</span>`).join(", ");
        return `<div class="tt-view"><span class="tt-vname">${v.name}</span><span class="tt-bar">${bar}</span></div>`
          + `<div class="tt-parts">${parts} <span class="muted">of ${total}</span></div>`
          + `<div class="tt-plates">${v.plates.map(plateText).join(" · ")}</div>`;
      }).join("")
      + `<div class="tt-score">Lethality <b>${Math.round(res.score * 100)}%</b> <span class="muted">(${res.shots} shots on the collision model, ${res.ms} ms; plates: first armour met, mean thickness, shots)</span></div>`;
  }
  const sbr = br(state.shooter);
  const d = shootable(id) && state.shooter ? br(id) - sbr : 0;
  const diff = state.shooter ? ` <span class="${d > 0 ? "up" : d < 0 ? "down" : ""}">(${d >= 0 ? "+" : ""}${d.toFixed(1)})</span>` : "";
  tip.innerHTML = `<div class="tt-title">${esc(name(id))}</div>`
    + `<div class="tt-sub">${u ? `${esc(nation(u.country))} · ${esc(u.role)}${u.rank ? ` · Rank ${RANKS[u.rank - 1]}` : ""}` : ""}${shootable(id) ? ` · BR ${br(id).toFixed(1)}${diff}` : ""}</div>`
    + (state.shooter ? `<div class="tt-shooter">shot by ${esc(name(state.shooter))}${id === state.shooter ? " (itself)" : ""}</div>` : "")
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
  history.replaceState(null, "", `?${p}`);
}

function esc(s: string) {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
}
