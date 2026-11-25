// Tech trees and names from the opened game data, unit icons from the wiki's static content; shot results from the Go simulation (shotdb.wasm) in a worker.
import type { Card, Grid, Outcome, Plate, Replies, Reply, Request, Source, TargetResult, Tree, Unit } from "./types";

const RANKS = ["I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X"];
const OUTCOMES: Outcome[] = ["kill", "pen", "shatter", "ricochet", "nonpen"];
const OUTCOME_LABEL: Record<Outcome, string> = { kill: "Pen + kill", pen: "Pen, no kill", shatter: "Pen, core broke", ricochet: "Ricochet", nonpen: "No pen" };

const params = new URLSearchParams(location.search);
const nationsParam = params.get("nations");
const state = {
  shooter: params.get("shooter") ?? "us_m4a2_sherman",
  nations: nationsParam ? new Set(nationsParam.split(",")) : null as Set<string> | null, // null: all
  speed: Number(params.get("speed") ?? 8), // targets per worker call
  game: new Map<string, Unit>(), // from the opened game data
  results: new Map<string, TargetResult>(),
  queue: [] as string[],
  done: 0,
  ms: 0, // simulation time of the done pairs
  gen: 0, // bumps on every queue restart; stale worker replies are dropped
  byLabel: new Map<string, string>(),
};

const $ = <T extends HTMLElement = HTMLElement>(s: string) => document.querySelector<T>(s)!;
// WT font glyphs (origin and event markers) that a normal font cannot draw
const clean = (name: string) => name.replace(/[␀-◿-]/g, "").trim();
const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

type Body<R> = R extends unknown ? Omit<R, "id"> : never;
type Call = { resolve: (r: Replies[keyof Replies]) => void; reject: (e: Error) => void };

const worker = new Worker(new URL("./worker.js", import.meta.url), { type: "module" });
const calls = new Map<number, Call>();
let callId = 0;
worker.onmessage = ({ data }: MessageEvent<Reply>) => {
  const c = calls.get(data.id)!;
  calls.delete(data.id);
  "error" in data ? c.reject(new Error(data.error)) : c.resolve(data);
};
worker.onerror = (e) => setStatus(`Worker failed: ${e.message}`, true);
const call = <K extends keyof Replies>(msg: Extract<Body<Request>, { op: K }>) => new Promise<Replies[K]>((resolve, reject) => {
  const id = ++callId;
  calls.set(id, { resolve: resolve as Call["resolve"], reject });
  worker.postMessage({ ...msg, id });
});

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
setupPicker();
setupOpen();

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
  main.addEventListener("mouseleave", () => (tip.hidden = true));
  main.parentElement!.addEventListener("scroll", () => {
    if (!tip.classList.contains("pinned")) tip.hidden = true;
  }, { passive: true });
  main.addEventListener("mouseout", (e) => {
    if (!(e.relatedTarget instanceof Element && e.relatedTarget.closest("[data-id]"))) tip.hidden = true;
  });
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
}

function setupPicker() {
  const input = $<HTMLInputElement>("#shooter");
  input.addEventListener("change", () => {
    const id = state.byLabel.get(input.value);
    id ? selectShooter(id) : (input.value = pickerLabel(state.shooter));
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
    // Firefox: only a dropped folder gives file access without an upload
    $("#open").hidden = true;
    addEventListener("dragover", (e) => e.preventDefault());
    addEventListener("drop", async (e) => {
      e.preventDefault();
      const entry = e.dataTransfer?.items[0]?.webkitGetAsEntry();
      if (!entry?.isDirectory) return setStatus("Drop the War Thunder install folder", true);
      let files: Map<string, File>;
      try {
        setStatus(`Reading ${entry.name}…`);
        files = await installFiles(entry as FileSystemDirectoryEntry);
      } catch (err) {
        return setStatus(`Cannot read ${entry.name}: ${message(err)}`, true);
      }
      await openSource({ kind: "files", files }, entry.name);
    });
    return setStatus("Drop the War Thunder install folder on this page. This browser cannot remember it.");
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

// The files of an install that the simulation reads. A File reads nothing until asked.
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
  for (const path of ["aces.vromfs.bin", "char.vromfs.bin", "lang.vromfs.bin", "warthunder.yup"]) {
    const entry = await child(path, false);
    if (entry) await add(path, entry);
  }
  const tanks = "content/base/res/tanks";
  const dir = await child(tanks, true);
  if (dir) {
    const reader = (dir as FileSystemDirectoryEntry).createReader();
    // readEntries gives one batch per call and an empty batch at the end
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((ok, fail) => reader.readEntries(ok, fail));
      if (!batch.length) break;
      await Promise.all(batch.filter((e) => e.isFile && e.name.endsWith(".grp")).map((e) => add(`${tanks}/${e.name}`, e)));
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

async function openSource(source: Source, label: string) {
  const open = $<HTMLButtonElement>("#open");
  open.disabled = true;
  setStatus(`Loading ${label}…`);
  try {
    const { info } = await call({ op: "open", source });
    state.game = new Map(info.units.map((u) => [u.id, { ...u, name: clean(u.name) }]));
    trees = info.trees;
    render();
    const inTree = [...cards.keys()].filter((id) => state.game.has(id)).length;
    setStatus(`${label} · ${info.source}${info.version ? ` ${info.version}` : ""} · ${inTree} vehicles`);
    fillPicker();
    selectShooter(state.shooter);
    return true;
  } catch (err) {
    setStatus(message(err), true);
    return false;
  } finally {
    open.disabled = false;
  }
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
  const pick = shootable(id) ? id : [...cards.keys()].find(shootable);
  if (!pick) return;
  state.shooter = pick;
  $<HTMLInputElement>("#shooter").value = pickerLabel(pick);
  shooterInfo();
  document.querySelectorAll(".card.shooter").forEach((el) => el.classList.remove("shooter"));
  cards.get(pick)?.forEach((el) => el.classList.add("shooter"));
  for (const [uid, els] of cards) {
    els.forEach((el) => (el.querySelector(".card-br")!.textContent = shootable(uid) ? br(uid).toFixed(1) : "no BR"));
  }
  syncUrl();
  restartQueue();
}

function shooterInfo(note = "") {
  const s = state.game.get(state.shooter)!;
  $("#shooter-info").textContent = `${s.role}${s.rank ? ` · Rank ${RANKS[s.rank - 1]}` : ""} · BR ${s.br.toFixed(1)} · ${note || "click any card to shoot with it"}`;
}

// Outwards from the shooter's BR: by distance, the higher BR first on ties.
async function restartQueue() {
  const gen = ++state.gen;
  state.results.clear();
  state.done = 0;
  state.ms = 0;
  for (const els of cards.values()) {
    els.forEach((el) => {
      el.classList.remove("running", "done", "void");
      el.style.removeProperty("--bg");
      el.querySelectorAll<HTMLElement>(".card-views i").forEach((i) => i.style.removeProperty("background"));
    });
  }
  const sbr = br(state.shooter);
  state.queue = [...cards.keys()].filter(shootable).sort((a, b) => {
    const da = br(a) - sbr, db = br(b) - sbr;
    return Math.abs(da) - Math.abs(db) || db - da || a.localeCompare(b);
  });
  progress();
  while (state.done < state.queue.length) {
    const batch = state.queue.slice(state.done, state.done + state.speed);
    batch.forEach((id) => cards.get(id)!.forEach((el) => el.classList.add("running")));
    let results: TargetResult[];
    try {
      ({ results } = await call({ op: "sim", shooter: state.shooter, targets: batch }));
    } catch (err) {
      if (gen === state.gen) $("#progress-text").textContent = `Stopped: ${message(err)}`;
      return;
    }
    if (gen !== state.gen) return;
    const first = results[0];
    if (state.done === 0 && first && !("error" in first) && !first.shell) shooterInfo("no gun of 20 mm or more that attacks armour");
    for (const r of results) {
      state.results.set(r.target, r);
      if (!("error" in r)) state.ms += r.ms;
    }
    batch.forEach(paint);
    state.done += batch.length;
    progress();
  }
  const pin = params.get("hover");
  const pinned = pin ? cards.get(pin) : undefined;
  if (pinned) {
    pinned[0].scrollIntoView({ block: "center", inline: "center" });
    showTooltip(pinned[0], true);
  }
}

function progress() {
  const total = state.queue.length;
  $("#progress-fill").style.width = `${total ? (100 * state.done) / total : 0}%`;
  const next = state.queue[state.done];
  const avg = state.done ? ` · ${Math.round(state.ms / state.done)} ms a pair` : "";
  $("#progress-text").textContent = next
    ? `${state.done} / ${total} · now at BR ${br(next).toFixed(1)}${avg}`
    : `${total} / ${total} done${avg}`;
}

function paint(id: string) {
  const res = state.results.get(id);
  if (!res) return;
  for (const el of cards.get(id)!) {
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
  } else if (!res) {
    body = `<div class="tt-pending">Queued: ${state.queue.indexOf(id) - state.done + 1} ahead in line</div>`;
  } else if ("error" in res) {
    body = `<div class="tt-pending">Failed: ${esc(res.error)}</div>`;
  } else if (!res.shell) {
    body = `<div class="tt-pending">The shooter has no gun of 20 mm or more that attacks armour</div>`;
  } else {
    body = `<div class="tt-shell">${esc(res.shell.name)} <span class="muted">${shellType(res.shell.type)} · ${res.shell.pen} mm flat at 500 m</span></div>`
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
  const d = shootable(id) ? br(id) - sbr : 0;
  tip.innerHTML = `<div class="tt-title">${esc(name(id))}</div>`
    + `<div class="tt-sub">${u ? `${esc(nation(u.country))} · ${esc(u.role)}${u.rank ? ` · Rank ${RANKS[u.rank - 1]}` : ""}` : ""}${shootable(id) ? ` · BR ${br(id).toFixed(1)} <span class="${d > 0 ? "up" : d < 0 ? "down" : ""}">(${d >= 0 ? "+" : ""}${d.toFixed(1)})</span>` : ""}</div>`
    + `<div class="tt-shooter">shot by ${esc(name(state.shooter))}${id === state.shooter ? " (itself)" : ""}</div>`
    + body;
  tip.hidden = false;
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
  p.set("shooter", state.shooter);
  p.delete("mode");
  !state.nations || state.nations.size === trees.length ? p.delete("nations") : p.set("nations", [...state.nations].join(","));
  history.replaceState(null, "", `?${p}`);
}

function esc(s: string) {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
}
