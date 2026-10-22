// The working tree field: a path input, recent paths, OPEN, and a status chip.
// The server's root wins on load; recents are a client convenience kept in
// localStorage, merged with the folders the server suggests.

const STORAGE_KEY = 'awv.trees';
const RECENT_LIMIT = 6;

export type TreeStatus = 'ok' | 'busy' | 'err';

export interface TreeField {
  setRoot(root: string): void;
  setStatus(text: string, kind: TreeStatus, detail?: string): void;
  remember(path: string): void;
  suggest(paths: string[]): void;
}

function loadRecents(): string[] {
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '[]');
    return Array.isArray(saved) ? saved.filter((item): item is string => typeof item === 'string' && item !== '').slice(0, RECENT_LIMIT) : [];
  } catch {
    return [];
  }
}

export function bindTree(form: HTMLFormElement, input: HTMLInputElement, list: HTMLDataListElement, status: HTMLElement, open: (path: string) => void): TreeField {
  let recents = loadRecents();
  let suggestions: string[] = [];

  const renderList = () => {
    list.textContent = '';
    for (const path of [...new Set([...recents, ...suggestions])]) {
      const option = document.createElement('option');
      option.value = path;
      list.appendChild(option);
    }
  };

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    const path = input.value.trim().replace(/(.)[\\/]+$/, '$1');
    if (!path) {
      field.setStatus('ENTER A PATH', 'err');
      return;
    }
    open(path);
  });

  const field: TreeField = {
    setRoot(root) {
      if (document.activeElement !== input) input.value = root;
    },
    setStatus(text, kind, detail) {
      status.textContent = text;
      status.className = kind === 'ok' ? 'tstat' : `tstat ${kind}`;
      status.title = detail ?? text;
    },
    remember(path) {
      recents = [path, ...recents.filter((item) => item !== path)].slice(0, RECENT_LIMIT);
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(recents));
      } catch {
        // Without storage the recents last until the page reloads.
      }
      renderList();
    },
    suggest(paths) {
      suggestions = paths;
      renderList();
    },
  };
  renderList();
  return field;
}
