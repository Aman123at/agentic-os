// The Browser's page view: a canvas showing the JPEG frames Chromium streams
// from inside the Machine over /ws/browser (PLAN.md M5.2), and the mouse,
// wheel and keyboard sent back. Kept apart from React like the Terminal's
// xterm (§4.3 rule 6): frames go straight onto the canvas, never through React
// state. React hears only the toolbar state and notices, which change rarely.

export interface PageState {
  url: string;
  title: string;
  loading: boolean;
  canBack: boolean;
  canForward: boolean;
  /** The Task whose Agent is using the page, if any (PLAN.md M5.3). */
  agent: string;
}

export type Status = "connecting" | "open" | "closed";

export interface PageOptions {
  onState: (s: PageState) => void;
  onNotice: (message: string) => void;
  onStatus: (s: Status) => void;
  // Keyboard shortcuts the window handles itself (address bar, reload…).
  onShortcut: (name: "address" | "reload" | "back" | "forward") => void;
}

export type Command =
  | { type: "navigate"; url: string }
  | { type: "back" | "forward" | "reload" | "stop" }
  | { type: "visible"; visible: boolean }
  | { type: "resize"; width: number; height: number; scale: number };

// The page is never streamed above 2x: a big window's frames are already large.
const MAX_SCALE = 2;
const MAC = /Mac|iPhone|iPad/.test(navigator.platform);

// Modifier bits as DevTools counts them.
function modifiers(e: KeyboardEvent | MouseEvent): number {
  return (e.altKey ? 1 : 0) | (e.ctrlKey ? 2 : 0) | (e.metaKey ? 4 : 0) | (e.shiftKey ? 8 : 0);
}

const BUTTONS = ["left", "middle", "right", "back", "forward"];

// Editing shortcuts Chromium on Linux doesn't bind to ⌘; paste is left to the
// browser's own paste event, which carries the Host clipboard's text.
const EDIT: Record<string, string> = { a: "selectAll", c: "copy", x: "cut", z: "undo", y: "redo" };

function socketURL(): string {
  const scheme = window.location.protocol === "https:" ? "wss" : "ws";
  return `${scheme}://${window.location.host}/ws/browser`;
}

export class PageView {
  private opts: PageOptions;
  private ws?: WebSocket;
  private canvas?: HTMLCanvasElement;
  private ctx?: CanvasRenderingContext2D | null;
  private keys?: HTMLTextAreaElement;
  private ro?: ResizeObserver;
  private cleanup: Array<() => void> = [];
  private disposed = false;
  private visible = true;
  private size = { width: 0, height: 0, scale: 1 };
  private resizeTimer = 0;
  // Frame decoding: at most one at a time, and only the newest one waiting.
  private decoding = false;
  private waiting?: Blob;
  // Pointer moves are sent at most once per animation frame.
  private move?: PointerEvent;
  private moveFrame = 0;
  private drawn = false;
  private composing = false;

  constructor(opts: PageOptions) {
    this.opts = opts;
  }

  // mount wires the canvas and the hidden textarea that takes keyboard input
  // (it gives the page input methods and paste), then connects.
  mount(canvas: HTMLCanvasElement, keys: HTMLTextAreaElement): void {
    this.canvas = canvas;
    this.ctx = canvas.getContext("2d", { alpha: false });
    // An opaque canvas starts black; a page that hasn't drawn yet looks white.
    if (this.ctx) {
      this.ctx.fillStyle = "#fff";
      this.ctx.fillRect(0, 0, canvas.width, canvas.height);
    }
    this.keys = keys;
    this.ro = new ResizeObserver(() => this.measure());
    this.ro.observe(canvas);
    this.measure(true);
    this.listen();
    this.connect();
  }

  connect(): void {
    if (this.disposed) return;
    this.ws?.close();
    this.drawn = false;
    this.opts.onStatus("connecting");
    const ws = new WebSocket(socketURL());
    ws.binaryType = "blob";
    this.ws = ws;
    ws.onopen = () => {
      this.opts.onStatus("open");
      this.send({ type: "resize", ...this.size });
      if (!this.shown()) this.send({ type: "visible", visible: false });
    };
    ws.onmessage = (e) => {
      if (e.data instanceof Blob) {
        this.frame(e.data);
        return;
      }
      let msg: { type?: string; message?: string } & Partial<PageState>;
      try {
        msg = JSON.parse(e.data as string);
      } catch {
        return;
      }
      if (msg.type === "state") {
        this.opts.onState({
          url: msg.url ?? "",
          title: msg.title ?? "",
          loading: Boolean(msg.loading),
          canBack: Boolean(msg.canBack),
          canForward: Boolean(msg.canForward),
          agent: msg.agent ?? "",
        });
      } else if (msg.type === "notice" && msg.message) {
        this.opts.onNotice(msg.message);
      }
    };
    ws.onclose = () => {
      if (this.ws === ws && !this.disposed) this.opts.onStatus("closed");
    };
  }

  send(cmd: Command | Record<string, unknown>): void {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(cmd));
  }

  // setVisible pauses the stream while the window is minimized or the tab hidden.
  setVisible(visible: boolean): void {
    if (visible === this.visible) return;
    this.visible = visible;
    this.send({ type: "visible", visible: this.shown() });
  }

  private shown(): boolean {
    return this.visible && !document.hidden;
  }

  focus(): void {
    this.keys?.focus({ preventScroll: true });
  }

  dispose(): void {
    this.disposed = true;
    window.clearTimeout(this.resizeTimer);
    cancelAnimationFrame(this.moveFrame);
    this.ro?.disconnect();
    for (const off of this.cleanup) off();
    this.ws?.close();
  }

  // measure sizes the canvas's backing store to the page's pixels and tells the
  // browser the new viewport, debounced while a window edge is dragged.
  private measure(now = false): void {
    const c = this.canvas;
    if (!c) return;
    const box = c.getBoundingClientRect();
    if (box.width < 1 || box.height < 1) return;
    const scale = Math.min(MAX_SCALE, Math.max(1, window.devicePixelRatio || 1));
    const width = Math.round(box.width);
    const height = Math.round(box.height);
    if (width === this.size.width && height === this.size.height && scale === this.size.scale) return;
    this.size = { width, height, scale };
    window.clearTimeout(this.resizeTimer);
    const apply = () => this.send({ type: "resize", ...this.size });
    if (now) apply();
    else this.resizeTimer = window.setTimeout(apply, 120);
  }

  private frame(blob: Blob): void {
    if (this.decoding) {
      this.waiting = blob;
      return;
    }
    this.decoding = true;
    void createImageBitmap(blob)
      .then((bmp) => {
        const c = this.canvas;
        if (c && this.ctx && !this.disposed) {
          if (c.width !== bmp.width || c.height !== bmp.height) {
            c.width = bmp.width;
            c.height = bmp.height;
          }
          this.ctx.drawImage(bmp, 0, 0);
          if (!this.drawn) {
            this.drawn = true;
            c.dataset.drawn = "1";
          }
        }
        bmp.close();
      })
      .catch(() => {})
      .finally(() => {
        this.decoding = false;
        const next = this.waiting;
        this.waiting = undefined;
        if (next) this.frame(next);
      });
  }

  // point converts an event to page coordinates (CSS pixels of the viewport).
  private point(e: MouseEvent): { x: number; y: number } {
    const box = this.canvas!.getBoundingClientRect();
    const sx = this.size.width / (box.width || 1);
    const sy = this.size.height / (box.height || 1);
    return { x: (e.clientX - box.left) * sx, y: (e.clientY - box.top) * sy };
  }

  private on<K extends keyof HTMLElementEventMap>(
    el: HTMLElement,
    type: K,
    f: (e: HTMLElementEventMap[K]) => void,
    opts?: AddEventListenerOptions,
  ): void {
    el.addEventListener(type, f as EventListener, opts);
    this.cleanup.push(() => el.removeEventListener(type, f as EventListener, opts));
  }

  private listen(): void {
    const c = this.canvas!;
    const keys = this.keys!;

    const mouse = (event: "down" | "up" | "move", e: PointerEvent) => {
      this.send({
        type: "mouse",
        event,
        ...this.point(e),
        button: event === "move" ? "none" : (BUTTONS[e.button] ?? "none"),
        buttons: e.buttons,
        clicks: event === "move" ? 0 : Math.max(1, e.detail),
        modifiers: modifiers(e),
      });
    };
    this.on(c, "pointerdown", (e) => {
      e.preventDefault();
      c.setPointerCapture(e.pointerId);
      this.focus();
      mouse("down", e);
    });
    this.on(c, "pointerup", (e) => {
      if (c.hasPointerCapture(e.pointerId)) c.releasePointerCapture(e.pointerId);
      mouse("up", e);
    });
    this.on(c, "pointermove", (e) => {
      this.move = e;
      if (this.moveFrame) return;
      this.moveFrame = requestAnimationFrame(() => {
        this.moveFrame = 0;
        if (this.move) mouse("move", this.move);
        this.move = undefined;
      });
    });
    this.on(c, "contextmenu", (e) => e.preventDefault());
    this.on(
      c,
      "wheel",
      (e) => {
        e.preventDefault();
        const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? this.size.height : 1;
        this.send({ type: "wheel", ...this.point(e), deltaX: e.deltaX * unit, deltaY: e.deltaY * unit, modifiers: modifiers(e) });
      },
      { passive: false },
    );

    const key = (event: "down" | "up", e: KeyboardEvent) => {
      if (e.isComposing || e.keyCode === 229) return; // an input method is composing
      const mod = MAC ? e.metaKey : e.ctrlKey;
      const k = e.key.toLowerCase();
      if (mod && !e.altKey) {
        const shortcut = k === "l" ? "address" : k === "r" ? "reload" : k === "[" ? "back" : k === "]" ? "forward" : undefined;
        if (shortcut) {
          e.preventDefault();
          if (event === "down") this.opts.onShortcut(shortcut);
          return;
        }
        // ⌘V: let the paste event below carry the Host clipboard.
        if (k === "v") return;
      }
      e.preventDefault();
      const printable = [...e.key].length === 1 && !e.ctrlKey && !e.metaKey;
      const text = printable ? e.key : e.key === "Enter" ? "\r" : "";
      const commands: string[] = [];
      if (event === "down" && mod && EDIT[k]) commands.push(k === "z" && e.shiftKey ? "redo" : EDIT[k]);
      this.send({
        type: "key",
        event,
        key: e.key,
        code: e.code,
        text: event === "down" ? text : "",
        keyCode: e.keyCode,
        modifiers: modifiers(e),
        commands,
      });
    };
    this.on(keys, "keydown", (e) => key("down", e));
    this.on(keys, "keyup", (e) => key("up", e));
    this.on(keys, "compositionstart", () => {
      this.composing = true;
    });
    this.on(keys, "compositionend", (e) => {
      this.composing = false;
      if (e.data) this.send({ type: "text", text: e.data });
      keys.value = "";
    });
    this.on(keys, "input", () => {
      // What an input method typed was sent on compositionend.
      if (!this.composing) keys.value = "";
    });
    this.on(keys, "paste", (e) => {
      e.preventDefault();
      const text = e.clipboardData?.getData("text/plain");
      if (text) this.send({ type: "text", text });
    });

    const onVisibility = () => this.send({ type: "visible", visible: this.shown() });
    document.addEventListener("visibilitychange", onVisibility);
    this.cleanup.push(() => document.removeEventListener("visibilitychange", onVisibility));
  }
}
