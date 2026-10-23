// Module and file names drawn over the graph. Module names sit on each
// module's ring; file names show only for the modules in focus, so a large
// tree does not fill with text.
import type { Vector3 } from 'three';
import type { Graph } from '../graph/load.ts';
import type { GraphScene } from '../view/scene.ts';

export class GroupLabels {
  private modules: HTMLElement[] = [];
  private files: HTMLElement[] = [];
  private fileModule: number[] = [];
  private readonly container: HTMLElement;

  constructor(container: HTMLElement) {
    this.container = container;
  }

  setGraph(graph: Graph, rootName: string): void {
    this.container.textContent = '';
    const span = (className: string, text: string) => {
      const element = document.createElement('span');
      element.className = className;
      element.textContent = text;
      element.hidden = true;
      this.container.appendChild(element);
      return element;
    };
    this.modules = graph.modules.map((name) => span('gmod', name === '.' ? rootName : name));
    this.files = graph.files.map((path) => span('gfile', path.split('/').pop() ?? path));
    this.fileModule = graph.files.map(() => -1);
    graph.fileOf.forEach((f, i) => (this.fileModule[f] = graph.modOf[i]));
  }

  render(scene: GraphScene, active: ReadonlySet<number>): void {
    const place = (element: HTMLElement, anchor: Vector3 | undefined, shown: boolean) => {
      const s = anchor && shown ? scene.toScreen(anchor) : null;
      element.hidden = !s || s.z > 1;
      if (s && !element.hidden) element.style.transform = `translate(${s.x.toFixed(1)}px,${s.y.toFixed(1)}px) translate(-50%,-50%)`;
    };
    this.modules.forEach((element, m) => place(element, scene.groups.modules[m], true));
    this.files.forEach((element, f) => place(element, scene.groups.files[f], active.has(this.fileModule[f])));
  }
}
