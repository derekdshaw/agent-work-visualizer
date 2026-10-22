import { isRecord } from '../session/events.ts';
import type { LayoutInput, LayoutResult } from './layout.ts';

// runLayout computes a layout in a worker, so a big graph does not stall the
// page. The worker starts with the first call and serves later calls too.
let worker: Worker | null = null;
let nextId = 0;
const pending = new Map<number, (result: LayoutResult) => void>();

function isLayoutResult(value: unknown): value is LayoutResult {
  return isRecord(value) && value['positions'] instanceof Float32Array && typeof value['floor'] === 'number' && typeof value['radius'] === 'number';
}

export function runLayout(input: LayoutInput): Promise<LayoutResult> {
  if (!worker) {
    worker = new Worker(new URL('./layout.worker.ts', import.meta.url), { type: 'module' });
    worker.addEventListener('message', (event: MessageEvent<unknown>) => {
      const data = event.data;
      if (!isRecord(data) || typeof data['id'] !== 'number' || !isLayoutResult(data['result'])) return;
      const resolve = pending.get(data['id']);
      pending.delete(data['id']);
      resolve?.(data['result']);
    });
  }
  const id = nextId++;
  const running = worker;
  return new Promise((resolve) => {
    pending.set(id, resolve);
    running.postMessage({ id, input });
  });
}
