// Session log lines, as internal/session writes them. The first line of a log
// is a Header and every later line an Ev. Paths are relative to the header's
// root, and node IDs match the graph's node IDs.

export interface Hunk {
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  // Each line starts with ' ' (context), '-' (removed), or '+' (added).
  lines: string[];
}

export interface Header {
  type: 'session';
  root: string;
  branch: string;
  t0: string;
  // Longest idle gap, in seconds, between two events on the timeline.
  gapCap: number;
}

export type TextType = 'title' | 'prompt' | 'plan' | 'step' | 'agent' | 'check_start';

interface EventBase {
  // Seconds on the session timeline.
  t: number;
  // Wall time, ISO 8601.
  at: string;
  agent: string;
}

export interface TextEvent extends EventBase {
  type: TextType;
  text: string;
}

export interface ReadEvent extends EventBase {
  type: 'read';
  path: string;
  line: number;
  nodeIds: string[];
}

export interface EditEvent extends EventBase {
  type: 'edit';
  path: string;
  // The first node is the edit target.
  nodeIds: string[];
  hunks: Hunk[];
  created: boolean;
}

export interface CheckEndEvent extends EventBase {
  type: 'check_end';
  ok: boolean;
}

export type Ev = TextEvent | ReadEvent | EditEvent | CheckEndEvent;

export type Result<T> = { ok: true; value: T } | { ok: false; error: string };

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function str(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  return typeof value === 'string' ? value : '';
}

function num(record: Record<string, unknown>, key: string): number {
  const value = record[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];
}

const textTypes: readonly string[] = ['title', 'prompt', 'plan', 'step', 'agent', 'check_start'];

function isTextType(value: string): value is TextType {
  return textTypes.includes(value);
}

function parseHunk(value: unknown): Hunk | null {
  if (!isRecord(value)) return null;
  return {
    oldStart: num(value, 'oldStart'),
    oldLines: num(value, 'oldLines'),
    newStart: num(value, 'newStart'),
    newLines: num(value, 'newLines'),
    lines: strings(value['lines']),
  };
}

// parseRecord turns one decoded log line into a header or an event.
export function parseRecord(value: unknown): Result<Header | Ev> {
  if (!isRecord(value)) return { ok: false, error: 'line is not an object' };
  const type = str(value, 'type');
  if (type === 'session') {
    return {
      ok: true,
      value: { type, root: str(value, 'root'), branch: str(value, 'branch'), t0: str(value, 't0'), gapCap: num(value, 'gapCap') || 30 },
    };
  }
  const base = { t: num(value, 't'), at: str(value, 'at'), agent: str(value, 'agent') };
  if (isTextType(type)) return { ok: true, value: { ...base, type, text: str(value, 'text') } };
  switch (type) {
    case 'read':
      return { ok: true, value: { ...base, type, path: str(value, 'path'), line: num(value, 'line'), nodeIds: strings(value['nodeIds']) } };
    case 'edit': {
      const hunks = Array.isArray(value['hunks']) ? value['hunks'].map(parseHunk).filter((hunk): hunk is Hunk => hunk !== null) : [];
      return {
        ok: true,
        value: { ...base, type, path: str(value, 'path'), nodeIds: strings(value['nodeIds']), hunks, created: value['created'] === true },
      };
    }
    case 'check_end':
      return { ok: true, value: { ...base, type, ok: value['ok'] === true } };
  }
  return { ok: false, error: `unknown event type ${JSON.stringify(type)}` };
}

// parseLine decodes one JSONL line.
export function parseLine(line: string): Result<Header | Ev> {
  let value: unknown;
  try {
    value = JSON.parse(line);
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : String(error) };
  }
  return parseRecord(value);
}

export interface ParsedLog {
  header: Header | null;
  events: Ev[];
  errors: string[];
}

// parseLog decodes a whole log. Lines that do not parse are reported and skipped.
export function parseLog(text: string): ParsedLog {
  const parsed: ParsedLog = { header: null, events: [], errors: [] };
  text.split('\n').forEach((line, index) => {
    if (!line.trim()) return;
    const result = parseLine(line);
    if (!result.ok) parsed.errors.push(`line ${index + 1}: ${result.error}`);
    else if (result.value.type === 'session') parsed.header = result.value;
    else parsed.events.push(result.value);
  });
  return parsed;
}
