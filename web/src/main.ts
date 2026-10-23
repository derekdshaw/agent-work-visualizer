import * as THREE from 'three';
import { runLayout } from './graph/layoutClient.ts';
import { buildGraph, fetchGraph, type Graph, type GraphPayload, hopsFrom, parseGraph } from './graph/load.ts';
import { connect, getJSON, getText, postJSON } from './net/live.ts';
import { type Ev, type Header, isRecord, parseLine, parseLog } from './session/events.ts';
import { fold, heatTarget, LOCK_T, nextEditTime, type Phase, previousEditTime, type ViewState } from './session/reduce.ts';
import { GroupLabels } from './ui/labels.ts';
import { type BlastItem, fmt, Panes } from './ui/panes.ts';
import { bindPanes } from './ui/split.ts';
import { bindTree } from './ui/tree.ts';
import { GraphScene } from './view/scene.ts';

function byId<T extends Element>(id: string, type: { new (): T; prototype: T }): T {
  const element = document.getElementById(id);
  if (!(element instanceof type)) throw new Error(`#${id} is missing from index.html`);
  return element;
}

const clamp = (value: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, value));
const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
// A press that moves less than this many pixels is a click, not an orbit.
const CLICK_SLOP = 6;

const stage = byId('stage', HTMLElement);
const scene = new GraphScene(byId('gl', HTMLCanvasElement), stage);
const panes = new Panes(
  {
    log: byId('log', HTMLElement),
    blast: byId('blast', HTMLElement),
    tabs: byId('tabs', HTMLElement),
    pathRoot: byId('pathRoot', HTMLElement),
    pathFile: byId('pathFile', HTMLElement),
    code: byId('code', HTMLElement),
    check: byId('chk', HTMLElement),
    live: byId('liveSt', HTMLElement),
    track: byId('track', HTMLElement),
    seek: byId('seek', HTMLInputElement),
  },
  reducedMotion,
);
bindPanes(byId('main', HTMLElement), byId('splitL', HTMLElement), byId('splitR', HTMLElement));
const labels = new GroupLabels(byId('glabels', HTMLElement));
const tree = bindTree(byId('treeForm', HTMLFormElement), byId('treeInput', HTMLInputElement), byId('treeList', HTMLDataListElement), byId('treeSt', HTMLElement), (path) => {
  void openTree(path);
});

const ui = {
  meta: byId('meta', HTMLElement),
  mismatch: byId('mismatch', HTMLElement),
  gate: byId('btnGate', HTMLButtonElement),
  rec: byId('rec', HTMLElement),
  recWrap: byId('recWrap', HTMLElement),
  sessionPick: byId('sessPick', HTMLSelectElement),
  sessionTitle: byId('sessTitle', HTMLElement),
  sessionBranch: byId('sessBranch', HTMLElement),
  focus: byId('focus', HTMLElement),
  cam: byId('cam', HTMLElement),
  card: byId('card', HTMLElement),
  cardName: byId('cardName', HTMLElement),
  cardInfo: byId('cardInfo', HTMLElement),
  lock: byId('lock', HTMLElement),
  lockStatus: byId('lockSt', HTMLElement),
  lockName: byId('lockName', HTMLElement),
  tip: byId('tip', HTMLElement),
  empty: byId('empty', HTMLElement),
  time: byId('time', HTMLElement),
  seek: byId('seek', HTMLInputElement),
  play: byId('btnPlay', HTMLButtonElement),
  previous: byId('btnPrev', HTMLButtonElement),
  next: byId('btnNext', HTMLButtonElement),
  speed: byId('btnSpeed', HTMLButtonElement),
  iconPause: byId('icoPause', SVGSVGElement),
  iconPlay: byId('icoPlay', SVGSVGElement),
  live: byId('btnLive', HTMLButtonElement),
  rotate: byId('btnRot', HTMLButtonElement),
  follow: byId('btnFollow', HTMLButtonElement),
  reset: byId('btnReset', HTMLButtonElement),
  cardClose: byId('cardX', HTMLButtonElement),
};

if (!scene.renderer) {
  const message = document.createElement('div');
  message.className = 'nogl';
  message.textContent = 'This view needs WebGL, which is unavailable here. The agent stream, diff, and scrubber still run.';
  stage.appendChild(message);
}

// ---- State --------------------------------------------------------------

interface Session {
  header: Header | null;
  events: Ev[];
}

let graph: Graph = buildGraph({ view: '', root: '', branch: '', version: 0, nodes: [], edges: [] });
let positions: Float32Array = new Float32Array(0);
let heat: Float32Array = new Float32Array(0);
let view = '';
let live: Session = { header: null, events: [] };
let replay: Session | null = null;
let closeStream: (() => void) | null = null;
let connected = false;
// switching is true while this page opens another folder, so the reset
// that the old view sends does not start a second reload.
let switching = false;
let graphGeneration = 0;
let paused = false;

// Time: in live mode t follows the live edge unless the user scrubs or pauses.
let simT = 0;
let total = 1;
let playing = true;
let followLive = true;
let scrubbing = false;
let autoRotate = !reducedMotion;
let followCamera = true;
let clock = 0;
let lastInteract = -10;

let hover = -1;
let selected = -1;
let reticleNode = -1;
let hopsKey = '';
let hops: Int32Array = new Int32Array(0);
let blastItems: BlastItem[] = [];

const session = (): Session => replay ?? live;

// ---- Graph ---------------------------------------------------------------

function shortName(i: number): string {
  const node = graph.payload.nodes[i];
  if (!node) return '';
  const parts = node.qualifiedName.split('.');
  return parts.length > 1 ? parts.slice(-2).join('.') : node.label || node.qualifiedName;
}

function labelOf(id: string): string {
  const i = graph.index.get(id);
  if (i !== undefined) return shortName(i);
  const name = id.split('\u0000').pop() ?? id;
  return name.split('.').slice(-2).join('.');
}

// applyGraph lays out payload and draws it. A refresh of the same view warm
// starts the layout from the current positions, so the picture stays put.
async function applyGraph(payload: GraphPayload, sameView: boolean): Promise<void> {
  const generation = ++graphGeneration;
  const next = buildGraph(payload);
  let previous: Float32Array | null = null;
  if (sameView && graph.n > 0) {
    previous = new Float32Array(next.n * 3).fill(Number.NaN);
    payload.nodes.forEach((node, i) => {
      const old = graph.index.get(node.id);
      if (old !== undefined) previous?.set(positions.subarray(old * 3, old * 3 + 3), i * 3);
    });
  }
  if (!sameView) tree.setStatus(`LAYOUT · ${next.n} NODES`, 'busy');
  const result = await runLayout({ n: next.n, edges: next.edges, modOf: next.modOf, moduleCount: next.modules.length, fileOf: next.fileOf, fileCount: next.files.length, previous, seed: 11 });
  if (generation !== graphGeneration) return;
  const oldHeat = heat;
  const oldGraph = graph;
  graph = next;
  positions = result.positions;
  heat = new Float32Array(next.n);
  payload.nodes.forEach((node, i) => {
    const old = oldGraph.index.get(node.id);
    if (sameView && old !== undefined) heat[i] = oldHeat[old];
  });
  hopsKey = '';
  hover = selected = reticleNode = -1;
  scene.setGraph(next, result, sameView);
  labels.setGraph(next, payload.root.split('/').pop() || '.');
  ui.meta.textContent = `${next.n} NODES · ${next.edges.length} EDGES`;
  ui.sessionBranch.textContent = payload.branch ? ` · ${payload.branch}` : '';
  tree.setRoot(payload.root);
  tree.setStatus(payload.root ? `INDEXED · ${next.n} NODES` : 'NO FOLDER', payload.root ? 'ok' : 'err');
  ui.empty.hidden = next.n > 0;
  ui.empty.textContent = payload.root ? 'No callables found in this working tree.' : 'Open a working tree to begin.';
  showCard();
}

function hopsFor(editNode: number): Int32Array {
  const key = `${graph.payload.version}|${graph.payload.view}|${editNode}`;
  if (key !== hopsKey) {
    hopsKey = key;
    hops = hopsFrom(graph.adj, editNode);
    const byDegree = (a: number, b: number) => graph.deg[b] - graph.deg[a];
    const ring = (hop: number) => [...hops.keys()].filter((i) => hops[i] === hop).sort(byDegree);
    blastItems = editNode < 0 ? [] : [
      ...ring(1).slice(0, 3).map((node) => ({ node, name: shortName(node), hop: 1 })),
      ...ring(2).slice(0, 2).map((node) => ({ node, name: shortName(node), hop: 2 })),
    ];
  }
  return hops;
}

// ---- Server --------------------------------------------------------------

async function load(): Promise<void> {
  const result = await fetchGraph();
  if (!result.ok) {
    tree.setStatus('SERVER UNREACHABLE', 'err', result.error);
    window.setTimeout(() => void load(), 2000);
    return;
  }
  await startView(result.value);
}

async function startView(payload: GraphPayload): Promise<void> {
  closeStream?.();
  view = payload.view;
  live = { header: null, events: [] };
  replay = null;
  ui.sessionPick.value = '';
  simT = 0;
  followLive = true;
  playing = true;
  await applyGraph(payload, false);
  closeStream = connect(view, {
    line(text) {
      const parsed = parseLine(text);
      if (!parsed.ok) return;
      if (parsed.value.type === 'session') live.header = parsed.value;
      else live.events.push(parsed.value);
    },
    control(name, data) {
      if (name === 'pause') setPause(data['paused'] === true, typeof data['waiting'] === 'number' ? data['waiting'] : 0);
      else if (name === 'graph') void refreshGraph(typeof data['version'] === 'number' ? data['version'] : 0);
      else if (name === 'status') {
        const message = typeof data['message'] === 'string' ? data['message'] : '';
        if (message) tree.setStatus('REFRESH FAILED', 'err', message);
        else tree.setStatus(`INDEXED · ${graph.n} NODES`, 'ok');
      } else if (name === 'mismatch') {
        const path = typeof data['path'] === 'string' ? data['path'] : '';
        ui.mismatch.hidden = path === '';
        ui.mismatch.textContent = `AGENT IN ${path}`;
        ui.mismatch.title = `Recent agent work in another worktree: ${path}`;
      }
    },
    reset() {
      if (!switching) void load();
    },
    connected(open) {
      connected = open;
    },
  });
  void refreshRoots();
  void refreshSessions();
}

async function refreshGraph(version: number): Promise<void> {
  if (version <= graph.payload.version) return;
  const result = await fetchGraph();
  if (result.ok && result.value.view === view) await applyGraph(result.value, true);
}

async function openTree(path: string): Promise<void> {
  tree.setStatus('REINDEXING…', 'busy');
  switching = true;
  const result = await postJSON('/api/root', { path });
  switching = false;
  if (!result.ok) {
    // The server keeps its folder, and so does the page.
    tree.setStatus('OPEN FAILED', 'err', result.error);
    return;
  }
  const parsed = parseGraph(result.value);
  if (!parsed.ok) {
    tree.setStatus('OPEN FAILED', 'err', parsed.error);
    return;
  }
  tree.remember(parsed.value.root);
  await startView(parsed.value);
}

async function refreshRoots(): Promise<void> {
  const result = await getJSON('/api/roots');
  if (!result.ok || !isRecord(result.value) || !Array.isArray(result.value['choices'])) return;
  tree.suggest(result.value['choices'].flatMap((choice) => (isRecord(choice) && typeof choice['path'] === 'string' ? [choice['path']] : [])));
}

let sessionsSig = '';

// refreshSessions rebuilds the picker only when the list changed, so a
// refresh on focus does not close a dropdown that is opening.
async function refreshSessions(): Promise<void> {
  const result = await getJSON('/api/sessions');
  if (!result.ok || !isRecord(result.value) || !Array.isArray(result.value['sessions'])) return;
  const sig = `${view}|${JSON.stringify(result.value['sessions'])}`;
  if (sig === sessionsSig) return;
  sessionsSig = sig;
  const current = ui.sessionPick.value;
  ui.sessionPick.textContent = '';
  const liveOption = document.createElement('option');
  liveOption.value = '';
  liveOption.textContent = 'LIVE';
  ui.sessionPick.appendChild(liveOption);
  for (const summary of result.value['sessions']) {
    if (!isRecord(summary) || typeof summary['name'] !== 'string' || summary['name'] === view) continue;
    const option = document.createElement('option');
    option.value = summary['name'];
    const started = typeof summary['t0'] === 'string' ? new Date(summary['t0']) : null;
    const title = typeof summary['title'] === 'string' && summary['title'] ? summary['title'] : 'untitled';
    const duration = typeof summary['duration'] === 'number' ? fmt(summary['duration']) : '';
    option.textContent = `${started ? started.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : summary['name']} · ${title} · ${duration}`;
    ui.sessionPick.appendChild(option);
  }
  ui.sessionPick.value = [...ui.sessionPick.options].some((option) => option.value === current) ? current : '';
}

ui.sessionPick.addEventListener('focus', () => void refreshSessions());
ui.sessionPick.addEventListener('change', () => {
  const name = ui.sessionPick.value;
  if (!name) {
    goLive();
    return;
  }
  void (async () => {
    const result = await getText(`/api/sessions/${encodeURIComponent(name)}`);
    if (!result.ok) {
      tree.setStatus('REPLAY FAILED', 'err', result.error);
      ui.sessionPick.value = '';
      return;
    }
    const parsed = parseLog(result.value);
    replay = { header: parsed.header, events: parsed.events };
    simT = 0;
    playing = true;
    followLive = false;
  })();
});

function goLive(): void {
  replay = null;
  ui.sessionPick.value = '';
  followLive = true;
  playing = true;
}

function setPause(next: boolean, waiting: number): void {
  paused = next;
  ui.gate.setAttribute('aria-pressed', String(paused));
  ui.gate.textContent = paused ? `RESUME AGENT${waiting ? ` · ${waiting} HELD` : ''}` : 'PAUSE AGENT';
  ui.gate.title = paused
    ? 'Agent tool calls wait at the PreToolUse hook until you resume.'
    : 'Hold the agent’s next tool call. Needs the PreToolUse hook from the README.';
}
ui.gate.addEventListener('click', () => {
  void postJSON(paused ? '/api/resume' : '/api/pause', undefined);
});

// ---- Scrubber ------------------------------------------------------------

function setPlayUI(): void {
  ui.iconPause.toggleAttribute('hidden', !playing);
  ui.iconPlay.toggleAttribute('hidden', playing);
  ui.play.setAttribute('aria-label', playing ? 'Pause session' : 'Play session');
}
ui.play.addEventListener('click', () => {
  playing = !playing;
  if (!playing) followLive = false;
  // Play at the end of a replay starts it over.
  if (playing && replay && simT >= total) simT = 0;
  setPlayUI();
});

// Playback speed multiplies how fast t advances while playing behind the live
// edge or in a replay. It is a per-viewer setting, so it lives in localStorage.
const SPEEDS = [1, 2, 4, 8, 16];
const SPEED_KEY = 'awv.speed';
let speed = 1;
try {
  const saved = Number(localStorage.getItem(SPEED_KEY));
  if (SPEEDS.includes(saved)) speed = saved;
} catch {
  // Without storage, playback starts at 1×.
}
function setSpeedUI(): void {
  ui.speed.textContent = `${speed}×`;
  ui.speed.setAttribute('aria-label', `Playback speed ${speed}×`);
  ui.speed.toggleAttribute('data-fast', speed > 1);
}
ui.speed.addEventListener('click', (event) => {
  const step = event.shiftKey ? SPEEDS.length - 1 : 1;
  speed = SPEEDS[(SPEEDS.indexOf(speed) + step) % SPEEDS.length];
  try {
    localStorage.setItem(SPEED_KEY, String(speed));
  } catch {
    // The speed then lasts until the page reloads.
  }
  setSpeedUI();
});

// Edit jumps land on the edit's start, so its lock-on and typing play.
function jumpTo(t: number): void {
  simT = t;
  followLive = false;
  playing = true;
}
ui.previous.addEventListener('click', () => {
  const t = previousEditTime(session().events, simT);
  jumpTo(t ?? 0);
});
ui.next.addEventListener('click', () => {
  const t = nextEditTime(session().events, simT);
  if (t !== null) jumpTo(t);
  else if (!replay) followLive = true;
});
ui.live.addEventListener('click', () => {
  if (replay) goLive();
  else {
    followLive = true;
    playing = true;
  }
});
ui.seek.addEventListener('pointerdown', () => (scrubbing = true));
// A drag can end anywhere on the page, not only over the track.
window.addEventListener('pointerup', () => (scrubbing = false));
window.addEventListener('pointercancel', () => (scrubbing = false));
ui.seek.addEventListener('blur', () => (scrubbing = false));
ui.seek.addEventListener('input', () => {
  const value = Number(ui.seek.value);
  simT = value;
  // Dragging to the end of a live session returns to the live edge.
  followLive = !replay && value >= Number(ui.seek.max) - 0.5;
});

// liveEnd is the current time on a live timeline: the last event's time plus
// the wall time since it, at most the log's gap cap, as the server counts.
function liveEnd(s: Session): number {
  const last = s.events[s.events.length - 1];
  if (!last) return 0;
  const since = (Date.now() - Date.parse(last.at)) / 1000;
  return last.t + clamp(Number.isFinite(since) ? since : 0, 0, s.header?.gapCap ?? 30);
}

// ---- Pointer and camera --------------------------------------------------

const pointers = new Map<number, { x: number; y: number }>();
let moved = 0;
let pinch = 0;
// A Shift drag pans instead of orbiting.
let panning = false;
let pointer: { x: number; y: number } | null = null;

function pinchDistance(): number {
  const [a, b] = [...pointers.values()];
  return a && b ? Math.hypot(a.x - b.x, a.y - b.y) : 0;
}

stage.addEventListener('pointerdown', (event) => {
  if (event.target instanceof Element && event.target.closest('button,.card')) return;
  stage.setPointerCapture(event.pointerId);
  pointers.set(event.pointerId, { x: event.clientX, y: event.clientY });
  moved = 0;
  lastInteract = clock;
  panning = event.shiftKey;
  if (pointers.size === 2) pinch = pinchDistance();
});
stage.addEventListener('pointermove', (event) => {
  const last = pointers.get(event.pointerId);
  if (last) {
    const dx = event.clientX - last.x;
    const dy = event.clientY - last.y;
    last.x = event.clientX;
    last.y = event.clientY;
    moved += Math.abs(dx) + Math.abs(dy);
    lastInteract = clock;
    if (pointers.size === 1 && panning) scene.orbit.pan(dx / scene.size.width, dy / scene.size.height);
    else if (pointers.size === 1) scene.orbit.rotate(dx, dy);
    else if (pointers.size === 2) {
      // Each finger moves the midpoint by half its own movement.
      scene.orbit.pan(dx / 2 / scene.size.width, dy / 2 / scene.size.height);
      const d = pinchDistance();
      if (pinch > 0 && d > 0) scene.orbit.zoom(pinch / d);
      pinch = d;
    }
  } else if (event.pointerType === 'mouse') {
    // Picking runs once per frame from the latest pointer position.
    pointer = { x: event.clientX, y: event.clientY };
  }
});
function endPointer(event: PointerEvent, click: boolean): void {
  if (!pointers.has(event.pointerId)) return;
  const count = pointers.size;
  pointers.delete(event.pointerId);
  if (click && count === 1 && moved < CLICK_SLOP) {
    selected = scene.nearest(event.clientX, event.clientY, 22);
    showCard();
  }
  if (pointers.size < 2) pinch = 0;
}
stage.addEventListener('pointerup', (event) => endPointer(event, true));
stage.addEventListener('pointercancel', (event) => endPointer(event, false));
stage.addEventListener('pointerleave', (event) => {
  if (event.pointerType === 'mouse') {
    pointer = null;
    hover = -1;
    stage.classList.remove('hov');
  }
});
stage.addEventListener(
  'wheel',
  (event) => {
    event.preventDefault();
    scene.orbit.zoom(Math.exp(event.deltaY * 0.0012));
    lastInteract = clock;
  },
  { passive: false },
);

ui.rotate.setAttribute('aria-pressed', String(autoRotate));
ui.rotate.addEventListener('click', () => {
  autoRotate = !autoRotate;
  ui.rotate.setAttribute('aria-pressed', String(autoRotate));
});
ui.follow.addEventListener('click', () => {
  followCamera = !followCamera;
  ui.follow.setAttribute('aria-pressed', String(followCamera));
});
ui.reset.addEventListener('click', () => {
  scene.orbit.reset();
  selected = -1;
  showCard();
});
ui.cardClose.addEventListener('click', () => {
  selected = -1;
  showCard();
});

function showCard(): void {
  const node = graph.payload.nodes[selected];
  if (!node) {
    ui.card.hidden = true;
    return;
  }
  ui.card.hidden = false;
  ui.cardName.textContent = node.qualifiedName;
  const hop = hops[selected];
  const distance = hop === undefined || hop >= 99 ? 'not connected to the edit' : hop === 0 ? 'edit target' : `${hop} ${hop === 1 ? 'hop' : 'hops'} from the edit`;
  ui.cardInfo.textContent = `${node.file}:${node.line}\n${node.kind} · ${node.module} · ${graph.deg[selected]} links\n${distance}`;
}

// ---- Frame ---------------------------------------------------------------

const colors = {
  coral: new THREE.Color(0xff5a7e),
  lime: new THREE.Color(0x7cf5a6),
  cyan: new THREE.Color(0x4fe3f5),
};

function lockLabel(s: ViewState, scanning: boolean): string {
  if (!s.locked || scanning) return 'ACQUIRING';
  if (s.edit && s.edit.progress < 1) return 'EDITING';
  const labels: Partial<Record<Phase, string>> = { check: 'VERIFYING', clean: 'CLEAN', fail: 'FAILED' };
  return labels[s.phase] ?? 'LOCKED';
}

let last = performance.now();
let tick = 0;

function frame(now: number): void {
  // rAF timestamps can precede the last performance.now(); never run time backward.
  const dt = clamp((now - last) / 1000, 0, 0.05);
  last = now;
  clock += dt;
  const current = session();
  const events = current.events;
  const end = events.length ? events[events.length - 1].t : 0;
  total = replay ? Math.max(1, end + 2) : Math.max(1, liveEnd(current));
  if (!replay && followLive && !scrubbing) simT = liveEnd(current);
  else if (playing && !scrubbing) {
    simT += dt * speed;
    if (simT >= total) {
      simT = total;
      if (replay) playing = false;
      else followLive = true;
    }
  }
  simT = clamp(simT, 0, total);

  const s = fold(events, simT);
  const editId = s.nodeEdit?.event.nodeIds[0];
  const editNode = editId === undefined ? -1 : (graph.index.get(editId) ?? -1);
  const ring = hopsFor(editNode);
  const touched = new Set((s.nodeEdit?.event.nodeIds.slice(1) ?? []).map((id) => graph.index.get(id) ?? -1));
  const target = s.target === null ? -1 : (graph.index.get(s.target) ?? -1);
  const k = 1 - Math.exp(-dt * 6);
  for (let i = 0; i < graph.n; i++) {
    const goal = heatTarget(s, { hop: ring[i], reading: !s.locked && i === target, touched: touched.has(i), edited: s.edited.has(graph.payload.nodes[i].id) });
    heat[i] += (goal - heat[i]) * k;
  }

  // After an edit arrives, the reticle scans the target's busiest neighbors,
  // then locks on and shrinks from large to small.
  const scanning = s.locked && s.sinceTarget < LOCK_T;
  if (target >= 0) {
    if (scanning && s.sinceTarget >= 0.2) {
      const sequence = [...graph.adj[target]].sort((a, b) => graph.deg[b] - graph.deg[a]).slice(0, 3).concat(target);
      reticleNode = sequence[Math.min(sequence.length - 1, Math.floor((s.sinceTarget - 0.2) / ((LOCK_T - 0.2) / sequence.length)))];
    } else if (!scanning) reticleNode = target;
  } else reticleNode = -1;
  const lockedOn = s.locked && !scanning;
  const lockFactor = lockedOn ? 1 + 2 * Math.exp(-(s.sinceTarget - LOCK_T) * 5) : 1.25 + 0.08 * Math.sin(clock * 14);
  const reticleColor = !s.locked ? colors.cyan : s.phase === 'clean' ? colors.lime : s.phase === 'check' ? colors.cyan : colors.coral;

  if (pointer && pointers.size === 0) {
    hover = scene.nearest(pointer.x, pointer.y, 16);
    stage.classList.toggle('hov', hover >= 0);
  }
  scene.render({
    dt,
    time: clock,
    heat,
    hops: ring,
    editNode,
    reticleNode,
    lockFactor,
    locked: lockedOn,
    reticleColor,
    ripple: s.phase === 'edit' && s.edit !== null && s.edit.progress < 1 && !reducedMotion,
    hover,
    selected,
    follow: followCamera,
    rotate: autoRotate && pointers.size === 0 && clock - lastInteract > 2.5,
  });

  // Panes.
  panes.renderLog(events, s, labelOf);
  panes.renderBlast(blastItems, heat, editNode < 0 ? 'No edited callable yet.' : 'The edit target has no resolved callers or callees.');
  panes.renderTabs(s.files, s.edit?.event.path ?? '', (path) => {
    const latest = [...events.slice(0, s.count)].reverse().find((event) => event.type === 'edit' && event.path === path);
    if (latest) {
      simT = latest.t;
      followLive = false;
    }
  });
  panes.renderPath(graph.payload.root, s.edit?.event.path ?? '');
  panes.renderCode(s.edit);
  panes.renderStatus(s);
  panes.renderBursts(events, total);

  // Title bar and scrubber.
  ui.sessionTitle.textContent = s.title || '—';
  ui.recWrap.classList.toggle('replay', replay !== null);
  ui.rec.textContent = replay ? `REPLAY ${fmt(simT)}` : connected ? `REC ${fmt(simT)}` : 'OFFLINE';
  ui.time.textContent = `${fmt(simT)} / ${fmt(total)}`;
  ui.seek.max = String(total);
  if (!scrubbing) ui.seek.value = String(simT);
  ui.live.setAttribute('aria-pressed', String(!replay && followLive));
  setPlayUI();
  ui.focus.hidden = editNode < 0;
  ui.focus.textContent = `FOCUS · ${shortName(editNode)}`;

  // Overlays that follow the scene.
  const { width, height } = scene.size;
  if (reticleNode >= 0 && scene.renderer) {
    const sc = scene.toScreen(scene.reticlePosition);
    const radius = 0.0618 * lockFactor * height;
    ui.lock.hidden = false;
    const x = clamp(sc.x + radius * 0.75 + 14, 8, Math.max(8, width - ui.lock.offsetWidth - 8));
    const y = clamp(sc.y - radius * 0.75 - 34, 8, Math.max(8, height - ui.lock.offsetHeight - 8));
    ui.lock.style.transform = `translate(${x.toFixed(1)}px,${y.toFixed(1)}px)`;
    const status = lockLabel(s, scanning);
    const key = `${status}|${reticleNode}`;
    if (ui.lock.dataset['key'] !== key) {
      ui.lock.dataset['key'] = key;
      ui.lockStatus.textContent = status;
      ui.lockName.textContent = shortName(reticleNode);
      ui.lock.classList.toggle('ok', status === 'CLEAN');
      ui.lock.classList.toggle('scan', status === 'ACQUIRING');
    }
  } else ui.lock.hidden = true;
  if (scene.renderer) labels.render(scene, new Set([graph.modOf[hover], graph.modOf[editNode]].filter((m) => m !== undefined)));
  const hovered = hover >= 0 ? scene.position(hover) : null;
  if (hovered && pointers.size === 0) {
    const hs = scene.toScreen(hovered);
    ui.tip.hidden = false;
    const node = graph.payload.nodes[hover];
    ui.tip.textContent = node ? `${shortName(hover)} · ${node.file}:${node.line}` : '';
    ui.tip.style.transform = `translate(${clamp(hs.x + 12, 8, Math.max(8, width - ui.tip.offsetWidth - 8)).toFixed(1)}px,${clamp(hs.y + 10, 8, Math.max(8, height - ui.tip.offsetHeight - 8)).toFixed(1)}px)`;
  } else ui.tip.hidden = true;
  tick = (tick + 1) % 6;
  if (tick === 0) {
    ui.cam.textContent = scene.orbit.describe();
    if (selected >= 0) showCard();
  }
}

function loop(now: number): void {
  try {
    frame(now);
  } catch (error) {
    // A broken frame stops the loop; the console has the cause.
    console.error(error);
    tree.setStatus('VIEW ERROR', 'err', error instanceof Error ? error.message : String(error));
    return;
  }
  requestAnimationFrame(loop);
}

setPlayUI();
setSpeedUI();
void load();
requestAnimationFrame(loop);
