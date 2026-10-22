import { isRecord, type Result } from '../session/events.ts';

// The server streams one view's session log over server-sent events. Each
// "log" event is one JSONL line, and its event ID is the line number, so the
// browser's automatic reconnect resumes after the last line it received.
// Control events (pause, graph, status, mismatch) carry no ID. A "reset"
// event means the page belongs to another view and must start over.

export type ControlName = 'pause' | 'graph' | 'status' | 'mismatch';

export interface LiveHandlers {
  line(text: string): void;
  control(name: ControlName, data: Record<string, unknown>): void;
  reset(): void;
  connected(open: boolean): void;
}

const controls: readonly ControlName[] = ['pause', 'graph', 'status', 'mismatch'];

// connect streams the view and returns a function that closes the stream.
export function connect(view: string, handlers: LiveHandlers): () => void {
  const source = new EventSource(`/events?view=${encodeURIComponent(view)}`);
  source.addEventListener('log', (event) => handlers.line(event.data));
  for (const name of controls) {
    source.addEventListener(name, (event) => {
      const parsed = parseJSON(event.data);
      if (parsed.ok && isRecord(parsed.value)) handlers.control(name, parsed.value);
    });
  }
  source.addEventListener('reset', () => {
    source.close();
    handlers.reset();
  });
  source.addEventListener('open', () => handlers.connected(true));
  source.addEventListener('error', () => handlers.connected(false));
  return () => source.close();
}

function parseJSON(text: string): Result<unknown> {
  try {
    return { ok: true, value: JSON.parse(text) };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : String(error) };
  }
}

// postJSON posts body and returns the decoded answer, or the server's error
// text for a failed request.
export async function postJSON(path: string, body: unknown): Promise<Result<unknown>> {
  try {
    const response = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await response.text();
    if (!response.ok) return { ok: false, error: text.trim() || `${path}: ${response.status}` };
    return text ? parseJSON(text) : { ok: true, value: null };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : String(error) };
  }
}

export async function getJSON(path: string): Promise<Result<unknown>> {
  try {
    const response = await fetch(path, { cache: 'no-store' });
    if (!response.ok) return { ok: false, error: `${path}: ${response.status}` };
    return { ok: true, value: await response.json() };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : String(error) };
  }
}

export async function getText(path: string): Promise<Result<string>> {
  try {
    const response = await fetch(path, { cache: 'no-store' });
    if (!response.ok) return { ok: false, error: `${path}: ${response.status}` };
    return { ok: true, value: await response.text() };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : String(error) };
  }
}
