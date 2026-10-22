// Resizable side panels from the prototype. Drag a divider, or focus it and
// use Left/Right (16 px, 64 px with Shift); Home or a double click resets it.
// Widths persist in localStorage, and the graph keeps at least MIN_STAGE px.

const LEFT_DEFAULT = 290;
const RIGHT_DEFAULT = 390;
const LEFT_RANGE = [200, 640] as const;
const RIGHT_RANGE = [260, 1100] as const;
const MIN_STAGE = 320;
// The two dividers are 10 px each.
const GAPS = 20;
const STORAGE_KEY = 'awv.panes';

const clamp = (value: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, value));

export function bindPanes(main: HTMLElement, splitLeft: HTMLElement, splitRight: HTMLElement): void {
  let left = LEFT_DEFAULT;
  let right = RIGHT_DEFAULT;
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? 'null');
    if (typeof saved === 'object' && saved !== null && 'l' in saved && 'r' in saved && typeof saved.l === 'number' && typeof saved.r === 'number') {
      left = saved.l;
      right = saved.r;
    }
  } catch {
    // Storage can be unavailable or hold junk; the defaults apply.
  }

  const splitsShown = () => getComputedStyle(splitLeft).display !== 'none';

  function applyPanes(which?: 'l' | 'r'): void {
    if (!splitsShown()) return;
    const total = main.clientWidth;
    if (which === 'r') {
      right = clamp(right, RIGHT_RANGE[0], Math.min(RIGHT_RANGE[1], total - GAPS - MIN_STAGE - left));
    } else {
      left = clamp(left, LEFT_RANGE[0], Math.min(LEFT_RANGE[1], total - GAPS - MIN_STAGE - right));
      right = clamp(right, RIGHT_RANGE[0], Math.min(RIGHT_RANGE[1], total - GAPS - MIN_STAGE - left));
    }
    main.style.setProperty('--lw', `${Math.round(left)}px`);
    main.style.setProperty('--rw', `${Math.round(right)}px`);
    splitLeft.setAttribute('aria-valuenow', String(Math.round(left)));
    splitLeft.setAttribute('aria-valuemin', String(LEFT_RANGE[0]));
    splitLeft.setAttribute('aria-valuemax', String(LEFT_RANGE[1]));
    splitRight.setAttribute('aria-valuenow', String(Math.round(right)));
    splitRight.setAttribute('aria-valuemin', String(RIGHT_RANGE[0]));
    splitRight.setAttribute('aria-valuemax', String(RIGHT_RANGE[1]));
  }

  function savePanes(): void {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ l: Math.round(left), r: Math.round(right) }));
    } catch {
      // Without storage the widths last until the page reloads.
    }
  }

  function bindSplit(element: HTMLElement, which: 'l' | 'r'): void {
    const reset = () => {
      if (which === 'l') left = LEFT_DEFAULT;
      else right = RIGHT_DEFAULT;
      applyPanes(which);
      savePanes();
    };
    element.addEventListener('pointerdown', (event) => {
      element.setPointerCapture(event.pointerId);
      element.classList.add('drag');
      document.body.classList.add('resizing');
      event.preventDefault();
    });
    element.addEventListener('pointermove', (event) => {
      if (!element.classList.contains('drag')) return;
      const rect = main.getBoundingClientRect();
      if (which === 'l') left = event.clientX - rect.left - 5;
      else right = rect.right - event.clientX - 5;
      applyPanes(which);
    });
    const end = () => {
      if (!element.classList.contains('drag')) return;
      element.classList.remove('drag');
      document.body.classList.remove('resizing');
      savePanes();
    };
    element.addEventListener('pointerup', end);
    element.addEventListener('pointercancel', end);
    element.addEventListener('dblclick', reset);
    element.addEventListener('keydown', (event) => {
      const step = event.shiftKey ? 64 : 16;
      let delta = 0;
      if (event.key === 'ArrowLeft') delta = -step;
      else if (event.key === 'ArrowRight') delta = step;
      else if (event.key === 'Home') {
        event.preventDefault();
        reset();
        return;
      } else return;
      event.preventDefault();
      if (which === 'l') left += delta;
      else right -= delta;
      applyPanes(which);
      savePanes();
    });
  }

  bindSplit(splitLeft, 'l');
  bindSplit(splitRight, 'r');
  window.addEventListener('resize', () => applyPanes());
  applyPanes();
}
