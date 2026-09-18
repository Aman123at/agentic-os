// A single terminal pane: an xterm.js instance wired straight to a Session's
// WebSocket (/ws/session/<id>, PLAN.md §10). Kept deliberately apart from React:
// output bytes go straight into xterm and keystrokes straight out to the socket,
// never through React state (§4.3 rule 6) — that is what the keystroke-echo perf
// test guards. React only decides which pane is visible.
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";

import { ticket } from "../../api/auth";

// Two palettes, keyed off the Desktop theme; the ANSI colours match macOS's
// Terminal "Basic" scheme closely enough to feel at home.
const DARK = {
  background: "#1e1e22",
  foreground: "#e6e6ea",
  cursor: "#e6e6ea",
  selectionBackground: "#3a5f8a",
  black: "#1e1e22", red: "#ff5f57", green: "#5af78e", yellow: "#f3f99d",
  blue: "#57c7ff", magenta: "#ff6ac1", cyan: "#9aedfe", white: "#e6e6ea",
  brightBlack: "#7f7f86", brightRed: "#ff5f57", brightGreen: "#5af78e", brightYellow: "#f3f99d",
  brightBlue: "#57c7ff", brightMagenta: "#ff6ac1", brightCyan: "#9aedfe", brightWhite: "#ffffff",
} as const;
const LIGHT = {
  background: "#ffffff",
  foreground: "#1e1e22",
  cursor: "#1e1e22",
  selectionBackground: "#b4d5fe",
  black: "#1e1e22", red: "#c7262b", green: "#1a8f3c", yellow: "#8a6d00",
  blue: "#0a63c9", magenta: "#a333a3", cyan: "#0b8794", white: "#e6e6ea",
  brightBlack: "#7f7f86", brightRed: "#c7262b", brightGreen: "#1a8f3c", brightYellow: "#8a6d00",
  brightBlue: "#0a63c9", brightMagenta: "#a333a3", brightCyan: "#0b8794", brightWhite: "#1e1e22",
} as const;

function socketURL(id: string, ticket: string): string {
  const scheme = window.location.protocol === "https:" ? "wss" : "ws";
  return `${scheme}://${window.location.host}/ws/session/${encodeURIComponent(id)}?ticket=${encodeURIComponent(ticket)}`;
}

const encoder = new TextEncoder();

export interface TermOptions {
  id: string;
  // A watched Agent Session is read-only: no keystrokes and no resize are sent,
  // so watching never disturbs the Agent (PLAN.md §10, M3.3).
  readOnly: boolean;
  dark: boolean;
  onClose: () => void;
}

// TermController owns one pane's xterm instance, its WebSocket and its size
// observer for the pane's whole life. mount() attaches it to a DOM node;
// dispose() tears everything down.
export class TermController {
  private term: Terminal;
  private fit = new FitAddon();
  private ws?: WebSocket;
  private ro?: ResizeObserver;
  private opts: TermOptions;
  private closed = false;
  private lastCols = 0;
  private lastRows = 0;

  constructor(opts: TermOptions) {
    this.opts = opts;
    this.term = new Terminal({
      cursorBlink: !opts.readOnly,
      disableStdin: opts.readOnly,
      fontFamily: '"SF Mono", "JetBrains Mono", Menlo, Consolas, monospace',
      fontSize: 13,
      theme: opts.dark ? DARK : LIGHT,
      scrollback: 5000,
      allowProposedApi: true,
    });
    this.term.loadAddon(this.fit);
  }

  // mount opens the terminal onto el and connects the Session socket. Call once,
  // when the pane first becomes visible (a hidden element has no size to fit).
  mount(el: HTMLElement): void {
    this.term.open(el);
    try {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => webgl.dispose()); // fall back to the DOM renderer
      this.term.loadAddon(webgl);
    } catch {
      // WebGL is optional; the canvas/DOM renderer still works without it.
    }
    this.safeFit();

    // A test-only hook: the e2e harness sets window.__AOS_E2E__ before load, and
    // the perf suite reads this xterm instance to time keystroke echo from the
    // buffer (which the WebGL renderer would otherwise hide). Inert otherwise.
    if ((window as unknown as { __AOS_E2E__?: boolean }).__AOS_E2E__) {
      (window as unknown as { __aosTerm?: Terminal }).__aosTerm = this.term;
    }

    if (!this.opts.readOnly) {
      this.term.onData((data) => {
        if (this.ws?.readyState === WebSocket.OPEN) {
          this.ws.send(encoder.encode(data));
        }
      });
    }
    this.term.onResize(({ cols, rows }) => this.sendResize(cols, rows));

    this.ro = new ResizeObserver(() => this.safeFit());
    this.ro.observe(el);

    void this.connect();
  }

  private async connect(): Promise<void> {
    // A WebSocket upgrade cannot carry the Authorization header, so it rides a
    // single-use ticket in the query (ADR-0007, M6.5).
    let url: string;
    try {
      url = socketURL(this.opts.id, await ticket());
    } catch {
      if (!this.closed) this.opts.onClose();
      return;
    }
    if (this.closed) return;
    const ws = new WebSocket(url);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    ws.onopen = () => {
      // Tell the Session our size once the pipe is up (writers only).
      this.lastCols = 0;
      this.sendResize(this.term.cols, this.term.rows);
    };
    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) this.term.write(new Uint8Array(ev.data));
    };
    ws.onclose = () => {
      if (this.closed) return;
      this.term.write("\r\n\x1b[38;5;244m[the session ended]\x1b[0m\r\n");
      this.opts.onClose();
    };
    ws.onerror = () => ws.close();
  }

  private sendResize(cols: number, rows: number): void {
    if (this.opts.readOnly) return;
    if (cols === this.lastCols && rows === this.lastRows) return;
    this.lastCols = cols;
    this.lastRows = rows;
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: "resize", cols, rows }));
    }
  }

  private safeFit(): void {
    try {
      if (this.term.element?.offsetParent) this.fit.fit();
    } catch {
      // fit throws on a zero-size element; ignore until the pane has a size.
    }
  }

  // refit re-measures after the pane is shown or the window is resized.
  refit(): void {
    this.safeFit();
    this.term.focus();
  }

  setTheme(dark: boolean): void {
    this.opts.dark = dark;
    this.term.options.theme = dark ? DARK : LIGHT;
  }

  dispose(): void {
    this.closed = true;
    this.ro?.disconnect();
    this.ws?.close();
    this.term.dispose();
  }
}
