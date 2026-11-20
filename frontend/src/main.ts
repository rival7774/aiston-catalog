import {
  Component,
  ElementRef,
  HostListener,
  OnDestroy,
  OnInit,
  ViewChild,
  computed,
  signal,
} from "@angular/core";
import ru from "@angular/common/locales/ru";
import { CurrencyPipe, DatePipe, registerLocaleData } from "@angular/common";
import { bootstrapApplication } from "@angular/platform-browser";

interface Category {
  id: number;
  name: string;
  count: number;
  hasChildren: boolean;
}
interface Product {
  id: number;
  name: string;
  priceKopecks: number;
  createdAt: string;
}
interface Page {
  items: Product[];
  nextCursor: string | null;
}

async function get<T>(url: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch("/api/categories" + url, { signal });
  if (!response.ok)
    throw new Error(
      response.status === 404
        ? "Категория не найдена"
        : "Не удалось загрузить данные",
    );
  return response.json();
}

@Component({
  selector: "catalog-app",
  imports: [CurrencyPipe, DatePipe],
  templateUrl: "./view.html",
})
class Catalog implements OnInit, OnDestroy {
  readonly children = signal(new Map<number, Category[]>());
  readonly expanded = signal(new Set<number>());
  readonly treeErrors = signal(new Set<number>());
  readonly treeBusy = signal(new Set<number>());
  private pending = new Map<number, Promise<void>>();
  readonly rows = computed(() => {
    const result: { node: Category; depth: number }[] = [];
    const walk = (parent: number, depth: number) => {
      for (const node of this.children().get(parent) ?? []) {
        result.push({ node, depth });
        if (this.expanded().has(node.id)) walk(node.id, depth + 1);
      }
    };
    walk(0, 0);
    return result;
  });

  readonly selected = signal(0);
  readonly path = signal<Category[]>([]);
  readonly pathError = signal("");
  readonly items = signal<Product[]>([]);
  readonly busy = signal(false);
  readonly feedError = signal(false);
  readonly ended = signal(false);
  readonly top = signal(0);
  readonly height = signal(window.innerHeight);
  readonly rowHeight = 72;
  readonly start = computed(() =>
    Math.max(0, Math.floor(this.top() / this.rowHeight) - 6),
  );
  readonly visible = computed(() =>
    this.items().slice(
      this.start(),
      this.start() + Math.ceil(this.height() / this.rowHeight) + 14,
    ),
  );
  @ViewChild("viewport") viewport?: ElementRef<HTMLElement>;
  private request = new AbortController();
  readonly search = signal("");
  private timer?: ReturnType<typeof setTimeout>;
  filter(value: string) {
    clearTimeout(this.timer);
    this.search.set(value);
    this.request.abort();
    this.busy.set(true);
    this.items.set([]);
    this.ended.set(false);
    this.timer = setTimeout(() => {
      if (this.selected()) void this.select(this.selected(), "none");
    }, 300);
  }
  private cursor: string | null = null;

  ngOnInit() {
    void this.initialize();
  }
  ngOnDestroy() {
    clearTimeout(this.timer);
    this.request.abort();
  }

  load(parent: number): Promise<void> {
    if (this.children().has(parent)) return Promise.resolve();
    const existing = this.pending.get(parent);
    if (existing) return existing;
    this.treeBusy.update((s) => new Set(s).add(parent));
    this.treeErrors.update((s) => {
      const next = new Set(s);
      next.delete(parent);
      return next;
    });
    const task = get<Category[]>(parent ? "/" + parent + "/children" : "")
      .then((nodes) => {
        this.children.update((m) => new Map(m).set(parent, nodes));
      })
      .catch(() => {
        this.treeErrors.update((s) => new Set(s).add(parent));
      })
      .finally(() => {
        this.pending.delete(parent);
        this.treeBusy.update((s) => {
          const next = new Set(s);
          next.delete(parent);
          return next;
        });
      });
    this.pending.set(parent, task);
    return task;
  }

  toggle(node: Category) {
    const next = new Set(this.expanded());
    if (next.has(node.id)) next.delete(node.id);
    else {
      next.add(node.id);
      void this.load(node.id);
    }
    this.expanded.set(next);
  }

  @HostListener("window:popstate")
  async initialize() {
    const href = location.href;
    await this.load(0);
    if (href !== location.href || !this.children().has(0)) return;
    const raw = new URL(href).searchParams.get("category");
    const id = raw === null ? this.children().get(0)?.[0]?.id : Number(raw);
    if (!id || !Number.isInteger(id) || id < 1 || id > 2147483647) {
      this.request.abort();
      this.selected.set(0);
      this.path.set([]);
      this.pathError.set("Некорректный адрес категории");
      return;
    }
    this.select(id, raw === null ? "replace" : "none");
  }

  async select(id: number, mode: "push" | "replace" | "none" = "push") {
    clearTimeout(this.timer);
    this.request.abort();
    this.request = new AbortController();
    const signal = this.request.signal;
    const url = new URL(location.href);
    url.searchParams.set("category", String(id));
    if (mode === "replace") history.replaceState(null, "", url);
    if (mode === "push" && url.href !== location.href)
      history.pushState(null, "", url);
    this.selected.set(id);
    this.path.set([]);
    this.pathError.set("");
    this.items.set([]);
    this.top.set(0);
    this.ended.set(false);
    this.feedError.set(false);
    this.busy.set(false);
    this.cursor = null;
    if (this.viewport) this.viewport.nativeElement.scrollTop = 0;
    try {
      const path = await get<Category[]>("/" + id + "/path", signal);
      if (signal.aborted) return;
      this.path.set(path);
      const parents = path.slice(0, -1).map((c) => c.id);
      this.expanded.update((s) => new Set([...s, ...parents]));
      for (const parent of parents) void this.load(parent);
      void this.more();
    } catch (e) {
      if (!signal.aborted)
        this.pathError.set(e instanceof Error ? e.message : "Ошибка загрузки");
    }
  }

  async more() {
    if (this.busy() || this.ended() || !this.path().length) return;
    const signal = this.request.signal;
    this.busy.set(true);
    this.feedError.set(false);
    try {
      const params = new URLSearchParams();
      if (this.cursor) params.set("cursor", this.cursor);
      if (this.search().trim()) params.set("q", this.search().trim());
      const page = await get<Page>(
        "/" + this.selected() + "/products?" + params,
        signal,
      );
      if (signal.aborted) return;
      this.items.update((items) => items.concat(page.items));
      this.cursor = page.nextCursor;
      this.ended.set(!this.cursor);
    } catch {
      if (!signal.aborted) this.feedError.set(true);
    } finally {
      if (!signal.aborted) {
        this.busy.set(false);
        requestAnimationFrame(() => this.scrolled());
      }
    }
  }
  @HostListener("window:resize")
  scrolled() {
    const el = this.viewport?.nativeElement;
    if (!el) return;
    this.top.set(el.scrollTop);
    this.height.set(el.clientHeight);
    if (
      el.scrollHeight - el.scrollTop - el.clientHeight < 600 &&
      !this.feedError()
    )
      void this.more();
  }
}
registerLocaleData(ru);
bootstrapApplication(Catalog).catch(console.error);
