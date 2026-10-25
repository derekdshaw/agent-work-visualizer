import type { EditEvent, Ev, Hunk } from './events.ts';

// Everything on screen is a function of the session events and a time t:
// fold(events, t) gives the state, and heatTarget gives each node's glow in
// that state. Live view and replay differ only in where t comes from.

export type Phase = 'idle' | 'read' | 'plan' | 'edit' | 'check' | 'clean' | 'fail';

// Seconds that the reticle scans after an edit arrives before it locks on.
export const LOCK_T = 1.2;
// An edit types out at TYPE_RATE characters per second, within these bounds.
// A transcript records an edit only once it is complete, so the typing replays
// the real text at a fixed rate instead of the model's own pace.
export const TYPE_RATE = 90;
export const MIN_TYPE = 0.6;
export const MAX_TYPE = 6;
// Seconds without an event after which the agent counts as idle.
export const IDLE_AFTER = 20;

export interface EditView {
  event: EditEvent;
  // Position of the edit in the event list.
  index: number;
  // Seconds since the edit event.
  since: number;
  // Share of the added text typed out, 0 before the lock and 1 when done.
  progress: number;
}

export type CheckState = 'running' | 'ok' | 'fail';

export interface ViewState {
  t: number;
  // Number of events at or before t. The latest is events[count - 1].
  count: number;
  phase: Phase;
  // The node under the reticle: the target of the latest read or edit that
  // names a node. locked is true for an edit.
  target: string | null;
  locked: boolean;
  // Seconds since the event that set the target.
  sinceTarget: number;
  // The latest edit at or before t, which the code pane shows.
  edit: EditView | null;
  // The latest edit that names a node. The blast radius spreads from its
  // target, so an edit to a file without callables leaves it in place.
  nodeEdit: EditView | null;
  // Every node edited at or before t.
  edited: ReadonlySet<string>;
  check: { text: string; state: CheckState } | null;
  title: string;
  // Edited paths, the most recently edited last.
  files: string[];
}

export function addedChars(hunks: Hunk[]): number {
  let total = 0;
  for (const hunk of hunks) for (const line of hunk.lines) if (line.startsWith('+')) total += line.length - 1;
  return total;
}

export function typingDuration(event: EditEvent): number {
  return Math.min(MAX_TYPE, Math.max(MIN_TYPE, addedChars(event.hunks) / TYPE_RATE));
}

// countAt returns the number of events at or before t. The server writes
// events in time order, so a binary search finds the boundary.
export function countAt(events: readonly Ev[], t: number): number {
  let lo = 0;
  let hi = events.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (events[mid].t <= t) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

// previousEditTime returns the time of the latest edit before t, skipping an
// edit that started within the last REWIND_GRACE seconds, so repeated presses
// walk back instead of restarting the same edit. It returns null if none.
export const REWIND_GRACE = 0.5;
export function previousEditTime(events: readonly Ev[], t: number): number | null {
  for (let i = countAt(events, t - REWIND_GRACE) - 1; i >= 0; i--) {
    if (events[i].type === 'edit') return events[i].t;
  }
  return null;
}

// nextEditTime returns the time of the first edit after t, or null if none.
export function nextEditTime(events: readonly Ev[], t: number): number | null {
  for (let i = countAt(events, t); i < events.length; i++) {
    if (events[i].type === 'edit') return events[i].t;
  }
  return null;
}

// waitingSpans returns the spans where the agent waited for the user: from
// the last event before each prompt to the prompt.
export function waitingSpans(events: readonly Ev[]): Array<[number, number]> {
  const spans: Array<[number, number]> = [];
  for (let i = 1; i < events.length; i++) {
    if (events[i].type === 'prompt' && events[i].t > events[i - 1].t) spans.push([events[i - 1].t, events[i].t]);
  }
  return spans;
}

// thinkingSpans returns the spans where the agent thought before writing.
export function thinkingSpans(events: readonly Ev[]): Array<[number, number]> {
  return events.flatMap((event): Array<[number, number]> => (event.type === 'think' && event.since < event.t ? [[event.since, event.t]] : []));
}

function phaseOf(event: Ev): Phase | null {
  switch (event.type) {
    case 'read':
      return 'read';
    case 'edit':
      return 'edit';
    case 'check_start':
      return 'check';
    case 'check_end':
      return event.ok ? 'clean' : 'fail';
    case 'title':
      return null;
    case 'prompt':
    case 'plan':
    case 'think':
    case 'step':
    case 'agent':
      return 'plan';
  }
}

export function fold(events: readonly Ev[], t: number): ViewState {
  const count = countAt(events, t);
  const edited = new Set<string>();
  const files: string[] = [];
  let title = '';
  let phase: Phase = 'idle';
  let phaseTime = -Infinity;
  let targetEvent: Ev | null = null;
  let target: string | null = null;
  let edit: EditView | null = null;
  let nodeEdit: EditView | null = null;
  let check: ViewState['check'] = null;
  let checkText = '';
  for (let i = 0; i < count; i++) {
    const event = events[i];
    const next = phaseOf(event);
    if (next) {
      phase = next;
      phaseTime = event.t;
    }
    switch (event.type) {
      case 'title':
        title = event.text;
        break;
      case 'check_start':
        checkText = event.text;
        check = { text: checkText, state: 'running' };
        break;
      case 'check_end':
        check = { text: checkText, state: event.ok ? 'ok' : 'fail' };
        break;
      case 'edit':
        for (const id of event.nodeIds) edited.add(id);
        if (files.includes(event.path)) files.splice(files.indexOf(event.path), 1);
        files.push(event.path);
        edit = { event, index: i, since: 0, progress: 0 };
        if (event.nodeIds.length > 0) nodeEdit = edit;
        break;
    }
    if ((event.type === 'read' || event.type === 'edit') && event.nodeIds.length > 0) {
      targetEvent = event;
      target = event.nodeIds[0];
    }
  }
  for (const view of new Set([edit, nodeEdit])) {
    if (!view) continue;
    view.since = t - view.event.t;
    view.progress = Math.min(1, Math.max(0, (view.since - LOCK_T) / typingDuration(view.event)));
  }
  if (t - phaseTime > IDLE_AFTER) phase = 'idle';
  return {
    t,
    count,
    phase,
    target,
    locked: targetEvent?.type === 'edit',
    sinceTarget: targetEvent ? t - targetEvent.t : Infinity,
    edit,
    nodeEdit,
    edited,
    check,
    title,
    files,
  };
}

// NodeFlags describes one node relative to a state. hop is its distance from
// the target of the latest edit that names a node. reading marks the target of a read, touched
// another node of the latest edit, and edited a node edited at any time.
export interface NodeFlags {
  hop: number;
  reading: boolean;
  touched: boolean;
  edited: boolean;
}

// heatTarget is the glow that a node eases toward, from 0 to 1. The edit
// target lights first, its neighbors after the typing starts, and nodes two
// hops out a second later. The radius dims once a check passes or work stops.
export function heatTarget(s: ViewState, node: NodeFlags): number {
  let heat = 0;
  const edit = s.nodeEdit;
  if (edit) {
    const settled = s.phase === 'clean' || s.phase === 'idle';
    const typing = edit.progress < 1;
    if (node.hop === 0 || node.touched) {
      const own = edit.since < LOCK_T ? 0.7 : typing ? 1 : settled ? 0.45 : 0.8;
      heat = node.hop === 0 ? own : own * 0.8;
    } else if (node.hop === 1) {
      heat = edit.since < LOCK_T + 0.5 ? 0 : settled ? 0.35 : 0.75;
    } else if (node.hop === 2) {
      heat = edit.since < LOCK_T + 1.5 ? 0 : settled ? 0.2 : 0.5;
    }
  }
  if (node.reading) heat = Math.max(heat, s.phase === 'read' ? 0.55 : 0.35);
  if (node.edited) heat = Math.max(heat, 0.22);
  return heat;
}
