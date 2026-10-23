import type { Ev } from '../session/events.ts';
import { addedChars, type EditView, LOCK_T, type ViewState, typingDuration } from '../session/reduce.ts';

const LOG_ROWS = 7;
const CODE_ROW_LIMIT = 800;
// The typed diff redraws in this many steps, so a long edit costs a bounded
// number of DOM rebuilds.
const TYPE_STEPS = 48;

export function fmt(seconds: number): string {
  const t = Math.max(0, Math.floor(seconds));
  const pad = (value: number) => String(value).padStart(2, '0');
  const h = Math.floor(t / 3600);
  const m = Math.floor((t % 3600) / 60);
  return h > 0 ? `${h}:${pad(m)}:${pad(t % 60)}` : `${pad(m)}:${pad(t % 60)}`;
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, className?: string, text?: string): HTMLElementTagNameMap[K] {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function firstLine(text: string, limit = 160): string {
  const line = text.split('\n').find((item) => item.trim()) ?? '';
  return line.length > limit ? `${line.slice(0, limit - 1)}…` : line;
}

interface Row {
  tag: string;
  tone: string;
  text: string;
}

// describe returns the stream row of an event; a title has none, it names the session.
export function describe(event: Ev, label: (id: string) => string): Row | null {
  const who = event.agent && event.agent !== 'main' ? `[${event.agent.slice(0, 7)}] ` : '';
  const nodes = (ids: string[]) => (ids.length ? ` → ${label(ids[0])}${ids.length > 1 ? ` +${ids.length - 1}` : ''}` : '');
  switch (event.type) {
    case 'title':
      return null;
    case 'prompt':
      return { tag: 'ASK', tone: 't-cyan', text: who + firstLine(event.text) };
    case 'plan':
      return { tag: 'PLAN', tone: 't-cyan', text: who + firstLine(event.text) };
    case 'step':
      return { tag: 'TOOL', tone: 't-mute', text: who + firstLine(event.text) };
    case 'agent':
      return { tag: 'AGENT', tone: 't-lime', text: who + firstLine(event.text) };
    case 'check_start':
      return { tag: 'RUN', tone: 't-cyan', text: who + firstLine(event.text) };
    case 'check_end':
      return event.ok ? { tag: 'OK', tone: 't-lime', text: `${who}passed` } : { tag: 'FAIL', tone: 't-coral', text: `${who}failed` };
    case 'read':
      return { tag: 'READ', tone: 't-cyan', text: who + event.path + nodes(event.nodeIds) };
    case 'edit':
      return { tag: event.created ? 'NEW' : 'EDIT', tone: 't-coral', text: who + event.path + nodes(event.nodeIds) };
  }
}

export interface BlastItem {
  node: number;
  name: string;
  hop: number;
}

export interface PaneElements {
  log: HTMLElement;
  blast: HTMLElement;
  tabs: HTMLElement;
  pathRoot: HTMLElement;
  pathFile: HTMLElement;
  code: HTMLElement;
  check: HTMLElement;
  live: HTMLElement;
  track: HTMLElement;
  seek: HTMLInputElement;
}

export class Panes {
  private readonly e: PaneElements;
  private logSig = '';
  // Signatures start unmatched, so the first render draws each pane.
  private blastSig = '\u0000';
  private blastRows: Array<{ element: HTMLElement; node: number; lit: boolean }> = [];
  private tabsSig = '';
  private pathSig = '';
  private codeSig = '';
  private statusSig = '';
  private burstSig = '';
  private readonly reducedMotion: boolean;

  constructor(elements: PaneElements, reducedMotion: boolean) {
    this.e = elements;
    this.reducedMotion = reducedMotion;
  }

  renderLog(events: readonly Ev[], s: ViewState, label: (id: string) => string): void {
    const rows: Array<{ index: number; row: Row }> = [];
    for (let i = s.count - 1; i >= 0 && rows.length < LOG_ROWS; i--) {
      const row = describe(events[i], label);
      if (row) rows.unshift({ index: i, row });
    }
    const active = s.edit && s.edit.progress < 1 ? s.edit.index : -1;
    const sig = `${events.length > 0 ? events[0].at : ''}|${s.count}|${active}`;
    if (sig === this.logSig) return;
    this.logSig = sig;
    this.e.log.textContent = '';
    if (!rows.length) this.e.log.appendChild(el('div', 'none', 'Waiting for agent activity.'));
    for (const { index, row } of rows) {
      const line = el('div', index === active ? 'row active' : 'row');
      line.appendChild(el('span', 'ts', fmt(events[index].t)));
      const body = el('span');
      body.title = row.text;
      body.appendChild(el('b', `tag ${row.tone}`, row.tag));
      body.appendChild(document.createTextNode(` ${row.text}`));
      line.appendChild(body);
      this.e.log.appendChild(line);
    }
  }

  // renderBlast lists the nodes one and two hops from the edit target. empty
  // explains an empty list.
  renderBlast(items: BlastItem[], heat: Float32Array, empty: string): void {
    const sig = `${items.map((item) => `${item.node}:${item.hop}`).join(',')}|${empty}`;
    if (sig !== this.blastSig) {
      this.blastSig = sig;
      this.e.blast.textContent = '';
      this.blastRows = [];
      if (!items.length) this.e.blast.appendChild(el('div', 'none', empty));
      for (const item of items) {
        const row = el('div', 'brow');
        row.appendChild(el('span', undefined, item.name));
        row.appendChild(el('span', undefined, `${item.hop} ${item.hop === 1 ? 'hop' : 'hops'}`));
        this.e.blast.appendChild(row);
        this.blastRows.push({ element: row, node: item.node, lit: false });
      }
    }
    for (const row of this.blastRows) {
      const lit = heat[row.node] > 0.3;
      if (lit !== row.lit) {
        row.lit = lit;
        row.element.classList.toggle('lit', lit);
      }
    }
  }

  renderTabs(files: string[], selected: string, select: (path: string) => void): void {
    const sig = `${files.join('\n')}|${selected}`;
    if (sig === this.tabsSig) return;
    this.tabsSig = sig;
    this.e.tabs.textContent = '';
    for (const path of files.slice(-8)) {
      const tab = el('button', undefined, path.split('/').pop() ?? path);
      tab.type = 'button';
      tab.title = path;
      tab.setAttribute('role', 'tab');
      tab.setAttribute('aria-selected', String(path === selected));
      tab.addEventListener('click', () => select(path));
      this.e.tabs.appendChild(tab);
    }
  }

  renderPath(root: string, path: string): void {
    const sig = `${root}|${path}`;
    if (sig === this.pathSig) return;
    this.pathSig = sig;
    const separator = root.includes('\\') && !root.includes('/') ? '\\' : '/';
    this.e.pathRoot.textContent = root;
    this.e.pathFile.textContent = path ? separator + path.split('/').join(separator) : '';
  }

  // renderCode types out the latest edit: removed and context lines appear at
  // once, added text appears as progress grows, with a cursor while typing.
  renderCode(edit: EditView | null): void {
    if (!edit) {
      if (this.codeSig !== 'none') {
        this.codeSig = 'none';
        this.e.code.textContent = '';
        this.e.code.appendChild(el('div', 'none', 'The diff of the agent’s next edit streams here.'));
      }
      return;
    }
    const total = addedChars(edit.event.hunks);
    const step = Math.round(edit.progress * TYPE_STEPS);
    const budget = step >= TYPE_STEPS ? total : Math.floor((total * step) / TYPE_STEPS);
    const typing = edit.since >= LOCK_T && edit.progress < 1;
    const sig = `${edit.index}|${edit.event.t}|${budget}|${typing}|${edit.since < LOCK_T}`;
    if (sig === this.codeSig) return;
    this.codeSig = sig;
    const code = this.e.code;
    code.textContent = '';
    let remaining = edit.since < LOCK_T ? 0 : budget;
    let lastAdded: HTMLElement | null = null;
    let rows = 0;
    const addRow = (kind: string, number: number | string, sign: string, text: string) => {
      const row = el('div', kind ? `ln ${kind}` : 'ln');
      row.appendChild(el('span', 'no', String(number)));
      row.appendChild(el('span', 'sg', sign));
      const body = el('span', undefined, text);
      row.appendChild(body);
      code.appendChild(row);
      rows++;
      return body;
    };
    for (const [index, hunk] of edit.event.hunks.entries()) {
      if (rows >= CODE_ROW_LIMIT) break;
      if (index > 0) addRow('gap', '⋯', '', `@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`);
      let oldLine = hunk.oldStart;
      let newLine = hunk.newStart;
      for (const line of hunk.lines) {
        if (rows >= CODE_ROW_LIMIT) break;
        const text = line.slice(1);
        if (line.startsWith('+')) {
          if (remaining <= 0 && budget < total) continue;
          if (remaining <= 0 && total > 0 && budget === 0) continue;
          lastAdded = addRow('add', newLine++, '+', text.slice(0, Math.max(0, remaining)));
          remaining -= text.length;
        } else if (line.startsWith('-')) {
          addRow('del', oldLine++, '−', text);
        } else if (line.startsWith('\\')) {
          continue;
        } else {
          addRow('', newLine++, ' ', text);
          oldLine++;
        }
      }
    }
    if (rows >= CODE_ROW_LIMIT) code.appendChild(el('div', 'none', `Diff shortened to ${CODE_ROW_LIMIT} lines.`));
    const cursorLine = lastAdded;
    if (typing && cursorLine) {
      if (!this.reducedMotion) cursorLine.appendChild(el('span', 'cur'));
      const row = cursorLine.parentElement;
      if (row && (row.offsetTop < code.scrollTop || row.offsetTop > code.scrollTop + code.clientHeight - 40)) {
        code.scrollTop = row.offsetTop - code.clientHeight / 2;
      }
    }
  }

  renderStatus(s: ViewState): void {
    const typing = s.edit !== null && s.edit.since >= LOCK_T && s.edit.progress < 1;
    const sig = `${s.phase}|${typing}|${s.check?.text}|${s.check?.state}`;
    if (sig === this.statusSig) return;
    this.statusSig = sig;
    const check = this.e.check;
    check.textContent = '';
    check.appendChild(el('span', 't-cyan', 'CHECK'));
    const states = { running: 'running', ok: 'passed', fail: 'failed' };
    check.appendChild(document.createTextNode(s.check ? ` ${firstLine(s.check.text, 80)} · ${states[s.check.state]}` : ' · idle'));
    check.title = s.check?.text ?? '';
    const looks: Record<string, [string, string]> = {
      idle: ['var(--mute)', 'IDLE'],
      read: ['var(--cyan)', 'READING'],
      plan: ['var(--cyan)', 'PLANNING'],
      edit: ['var(--coral)', typing ? 'STREAMING' : 'EDITING'],
      check: ['var(--cyan)', 'VERIFYING'],
      clean: ['var(--lime)', 'CLEAN'],
      fail: ['var(--coral)', 'FAILED'],
    };
    const [color, label] = looks[s.phase];
    const dot = this.e.live.querySelector('i');
    const text = this.e.live.querySelector('span');
    if (dot) {
      dot.style.background = color;
      dot.style.boxShadow = `0 0 8px ${color}`;
    }
    if (text) {
      text.textContent = label;
      text.style.color = s.phase === 'edit' || s.phase === 'fail' ? 'var(--coral-soft)' : s.phase === 'clean' ? 'var(--lime)' : 'var(--mute)';
    }
  }

  // renderBursts marks each edit on the scrubber track, labelled with its file.
  renderBursts(events: readonly Ev[], total: number): void {
    const sig = `${events.length}|${events.length ? events[0].at : ''}|${Math.round(total)}`;
    if (sig === this.burstSig) return;
    this.burstSig = sig;
    for (const old of this.e.track.querySelectorAll('.burst,.tlab')) old.remove();
    if (total <= 0) return;
    let lastLabel = -1;
    for (const event of events) {
      if (event.type !== 'edit') continue;
      const start = Math.min(1, event.t / total);
      const width = Math.min(1 - start, (LOCK_T + typingDuration(event)) / total);
      const burst = el('div', 'burst');
      burst.style.left = `calc(7px + (100% - 14px) * ${start})`;
      burst.style.width = `calc((100% - 14px) * ${width})`;
      this.e.track.insertBefore(burst, this.e.seek);
      if (lastLabel < 0 || start - lastLabel > 0.12) {
        lastLabel = start;
        const name = el('div', 'tlab', event.path.split('/').pop() ?? event.path);
        name.style.left = `calc(7px + (100% - 14px) * ${start})`;
        this.e.track.insertBefore(name, this.e.seek);
      }
    }
  }
}
