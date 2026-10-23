import { isRecord } from '../session/events.ts';
import { layout, type LayoutInput } from './layout.ts';

// The page posts { id, input } and receives { id, result }.
function isLayoutInput(value: unknown): value is LayoutInput {
  return (
    isRecord(value) &&
    typeof value['n'] === 'number' &&
    Array.isArray(value['edges']) &&
    Array.isArray(value['modOf']) &&
    typeof value['moduleCount'] === 'number' &&
    Array.isArray(value['fileOf']) &&
    typeof value['fileCount'] === 'number' &&
    typeof value['seed'] === 'number' &&
    (value['previous'] === null || value['previous'] instanceof Float32Array)
  );
}

addEventListener('message', (event: MessageEvent<unknown>) => {
  const data = event.data;
  if (!isRecord(data) || typeof data['id'] !== 'number' || !isLayoutInput(data['input'])) return;
  const result = layout(data['input']);
  postMessage({ id: data['id'], result }, { transfer: [result.positions.buffer] });
});
