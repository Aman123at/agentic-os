// A fake aosd for Code's specs (PLAN.md M8.3–M8.5), in the page itself: an
// init script replaces fetch for aosd's routes with an in-memory Machine — a
// file tree, the event stream and CodeService — so the Desktop runs from a Vite
// dev server with no Docker. Unlike e2e-auth's route fakes it can hold the event
// stream open and push WorkspaceChanged events, which Code's live reloads need.
//
// A spec drives the Machine through window.__aosd (see FakeMachine): writing a
// file there is a change "made by something else", and the Workspace's watch
// reports it like inotify would.
import type { Page } from "@playwright/test";

export interface FakeOptions {
  rootMode?: boolean;
  // Files to start with, by absolute path; a path ending in "/" is a folder.
  files?: Record<string, string>;
  // Paths the Realm's user may read but not write (M8.4). In Standard Mode
  // anything outside the home folder is read-only too, as it is for aos.
  readOnly?: string[];
}

// FakeMachine is what a spec reaches through page.evaluate(() => window.__aosd).
export interface FakeMachine {
  write(path: string, text: string): void;
  remove(path: string): void;
  read(path: string): string | null;
  calls: string[];
  // The subscription ids handed out, newest last, and a way to drop the newest
  // stream as a network blip would.
  subscriptions: string[];
  dropStream(): void;
  dirty: Record<string, string[]>;
  workspaces: Record<string, { root: string; sub: string; open: boolean }>;
  // Confirmed changes to Sensitive System Files, and the versions they
  // replaced (M8.5).
  sensitiveWrites: string[];
  backups: Record<string, string[]>;
}

export async function installFakeAosd(page: Page, opts: FakeOptions = {}): Promise<void> {
  await page.addInitScript(() => {
    try {
      localStorage.setItem("aos.refresh", "refresh-token");
      sessionStorage.setItem("aos.layout", JSON.stringify({ theme: "light", windows: [], focused: "" }));
    } catch {
      // The spec fails loudly if the session is missing.
    }
  });
  await page.addInitScript(fake, opts);
}

// fake runs in the page. It must not reference anything outside itself.
function fake(opts: FakeOptions) {
  const HOME = "/home/aos";
  const enc = new TextEncoder();
  const dec = new TextDecoder();

  type Node = { dir: boolean; data: Uint8Array; mtime: number };
  const fs = new Map<string, Node>();
  let clock = Date.parse("2026-09-24T10:00:00Z");
  const tick = () => (clock += 7);
  const mkdirp = (p: string) => {
    for (let d = p; d && d !== "/"; d = parent(d)) if (!fs.has(d)) fs.set(d, { dir: true, data: new Uint8Array(), mtime: tick() });
  };
  const parent = (p: string) => (p.lastIndexOf("/") <= 0 ? "/" : p.slice(0, p.lastIndexOf("/")));
  const base = (p: string) => p.slice(p.lastIndexOf("/") + 1);
  fs.set("/", { dir: true, data: new Uint8Array(), mtime: tick() });
  mkdirp(HOME);
  for (const [p, text] of Object.entries(opts.files ?? {})) {
    if (p.endsWith("/")) mkdirp(p.slice(0, -1));
    else {
      mkdirp(parent(p));
      fs.set(p, { dir: false, data: enc.encode(text), mtime: tick() });
    }
  }

  const readOnly = new Set(opts.readOnly ?? []);
  // writable is what access(W_OK) says as the Realm's user; in Root Mode AOS's
  // own state is never writable (M8.4).
  const writable = (p: string) =>
    !readOnly.has(p) && (opts.rootMode ? !p.startsWith("/var/lib/aos") && !p.startsWith("/etc/aos") : p === HOME || p.startsWith(HOME + "/"));

  // ------------------------------------------------------------ the Machine
  type Sub = { id: string; push: (msg: unknown) => void; end: () => void };
  const subs: Sub[] = [];
  const workspaces: FakeMachine["workspaces"] = {};
  const machine: FakeMachine = {
    calls: [],
    subscriptions: [],
    dirty: {},
    workspaces,
    sensitiveWrites: [],
    backups: {},
    write(p, text) {
      const existed = fs.has(p);
      mkdirp(parent(p));
      fs.set(p, { dir: false, data: enc.encode(text), mtime: tick() });
      changed(existed ? "KIND_CHANGED" : "KIND_CREATED", p, false);
    },
    remove(p) {
      const node = fs.get(p);
      for (const k of [...fs.keys()]) if (k === p || k.startsWith(p + "/")) fs.delete(k);
      if (node) changed("KIND_DELETED", p, node.dir);
    },
    read(p) {
      const n = fs.get(p);
      return n && !n.dir ? dec.decode(n.data) : null;
    },
    dropStream() {
      subs.at(-1)?.end();
    },
  };
  (window as unknown as { __aosd: FakeMachine }).__aosd = machine;

  // changed tells each Workspace holding p, a little later, as a watch would.
  function changed(kind: string, p: string, dir: boolean) {
    setTimeout(() => {
      for (const [id, w] of Object.entries(workspaces)) {
        if (!w.open || !(p === w.root || p.startsWith(w.root + "/"))) continue;
        subs.find((s) => s.id === w.sub)?.push({ event: { time: new Date().toISOString(), workspace: { workspaceId: id, changes: [{ kind, path: p, dir }] } } });
      }
    }, 30);
  }

  // The fake's Sensitive System Files, in Root Mode only, as policy.Sensitive.
  const sensitive = (p: string): string => {
    if (!opts.rootMode) return "";
    if (p === "/etc/sudoers" || p.startsWith("/etc/sudoers.d/")) return "A mistake here can lock everyone out of sudo.";
    if (p === "/etc/fstab") return "A mistake here can stop the Machine from starting.";
    return "";
  };

  const info = (p: string) => {
    const n = fs.get(p)!;
    return {
      path: p,
      name: base(p) || "/",
      dir: n.dir,
      size: String(n.data.length),
      mode: n.dir ? 0o40000755 : 0o644,
      modifiedAt: new Date(n.mtime).toISOString(),
      writable: writable(p),
      sensitiveReason: sensitive(p),
    };
  };

  const abs = (p: string) => {
    if (p === "~") return HOME;
    if (p.startsWith("~/")) return HOME + p.slice(1);
    if (!p.startsWith("/")) return HOME + "/" + p;
    return p.replace(/\/+$/, "") || "/";
  };

  class RpcError {
    constructor(
      public code: string,
      public message: string,
      public details?: unknown[],
    ) {}
  }
  const notFound = (p: string) => new RpcError("not_found", `lstat ${p}: no such file or directory`);

  const b64 = (bytes: Uint8Array) => {
    let s = "";
    for (const b of bytes) s += String.fromCharCode(b);
    return btoa(s);
  };
  const unb64 = (s: string) => Uint8Array.from(atob(s || ""), (c) => c.charCodeAt(0));

  // A SensitivePath detail, encoded by hand: path = 1, reason = 2.
  const sensitiveDetail = (p: string, reason: string) => {
    const field = (tag: number, text: string) => {
      const b = enc.encode(text);
      const len: number[] = [];
      let n = b.length;
      do {
        len.push((n & 0x7f) | (n > 0x7f ? 0x80 : 0));
        n >>= 7;
      } while (n > 0);
      return [tag, ...len, ...b];
    };
    return { type: "aos.v1.SensitivePath", value: b64(new Uint8Array([...field(0x0a, p), ...field(0x12, reason)])).replace(/=+$/, "") };
  };
  // guard refuses an unconfirmed change to a Sensitive System File, as aosd does.
  const guard = (p: string, body: Record<string, unknown>): boolean => {
    const why = sensitive(p);
    if (why && !body.confirmSensitive) throw new RpcError("failed_precondition", `${p} is a Sensitive System File. ${why} Confirm to change it`, [sensitiveDetail(p, why)]);
    if (why) machine.sensitiveWrites.push(p);
    return Boolean(why);
  };

  let subCount = 0;
  let wsCount = 0;

  function unary(method: string, body: Record<string, unknown>): unknown {
    const str = (k: string) => String(body[k] ?? "");
    switch (method) {
      case "AuthService/Refresh":
        return { accessToken: "access", refreshToken: "refresh-token" };
      case "SystemService/Info":
        return { rootMode: Boolean(opts.rootMode), version: "fake" };
      case "TaskService/ListTasks":
        return { tasks: [] };
      case "ApprovalService/ListPending":
        return { approvals: [] };
      case "SystemService/ListNotifications":
        return { notifications: [] };
      case "SettingsService/GetDesktopState":
        return { state: "" };
      case "TrashService/ListTrash":
        return { items: [] };

      case "FileService/Stat": {
        const p = abs(str("path"));
        if (!fs.has(p)) throw notFound(p);
        return { info: info(p) };
      }
      case "FileService/List": {
        const p = abs(str("path"));
        if (!fs.get(p)?.dir) throw notFound(p);
        const kids = [...fs.keys()].filter((k) => k !== p && parent(k) === p);
        kids.sort((a, b) => Number(fs.get(b)!.dir) - Number(fs.get(a)!.dir) || base(a).localeCompare(base(b)));
        return { entries: kids.map(info) };
      }
      case "FileService/Read": {
        const p = abs(str("path"));
        const n = fs.get(p);
        if (!n || n.dir) throw notFound(p);
        const off = Number(body.offset ?? 0);
        const lim = Number(body.limit ?? 0) || 1 << 20;
        const part = n.data.slice(off, off + lim);
        return { content: b64(part), eof: off + part.length >= n.data.length };
      }
      case "FileService/Write": {
        const p = abs(str("path"));
        const exists = fs.has(p);
        if (exists && !body.overwrite) throw new RpcError("already_exists", `write ${p}: already exists`);
        if (!writable(p) || !writable(parent(p))) throw new RpcError("permission_denied", `open ${p}: permission denied`);
        if (guard(p, body) && exists) (machine.backups[p] ??= []).unshift(dec.decode(fs.get(p)!.data));
        mkdirp(parent(p));
        fs.set(p, { dir: false, data: unb64(str("content")), mtime: tick() });
        changed(exists ? "KIND_CHANGED" : "KIND_CREATED", p, false);
        return {};
      }
      case "FileService/MakeFolder": {
        const p = abs(str("path"));
        if (fs.has(p)) throw new RpcError("already_exists", `mkdir ${p}: already exists`);
        guard(p, body);
        mkdirp(p);
        changed("KIND_CREATED", p, true);
        return {};
      }
      case "FileService/Move": {
        const from = abs(str("source"));
        const to = abs(str("destination"));
        if (!fs.has(from)) throw notFound(from);
        if (fs.has(to)) throw new RpcError("already_exists", `rename ${to}: already exists`);
        if (!guard(from, body)) guard(to, body);
        for (const k of [...fs.keys()]) {
          if (k === from || k.startsWith(from + "/")) {
            fs.set(to + k.slice(from.length), fs.get(k)!);
            fs.delete(k);
          }
        }
        changed("KIND_DELETED", from, fs.get(to)!.dir);
        changed("KIND_CREATED", to, fs.get(to)!.dir);
        return {};
      }
      case "FileService/Delete": {
        const p = abs(str("path"));
        if (!fs.has(p)) throw notFound(p);
        guard(p, body);
        machine.remove(p);
        return { item: { id: p, name: base(p) } };
      }

      case "FileService/CheckSensitive": {
        const p = abs(str("path"));
        if (!sensitive(p) || p !== "/etc/sudoers") return {};
        const text = dec.decode(unb64(str("content")));
        return text.includes("ALL") ? { checker: "visudo -c", passed: true, output: "/etc/sudoers: parsed OK" } : { checker: "visudo -c", output: `/etc/sudoers:1:1: syntax error\n${text}` };
      }
      case "FileService/ListSensitiveBackups": {
        const p = abs(str("path"));
        return { backups: (machine.backups[p] ?? []).map((_, i) => ({ id: `v${i}`, savedAt: new Date(clock - i * 1000).toISOString(), size: "1" })) };
      }
      case "FileService/RestoreSensitiveBackup": {
        const p = abs(str("path"));
        const kept = machine.backups[p] ?? [];
        const old = kept[Number(str("id").slice(1))];
        if (old === undefined) throw new RpcError("not_found", "no such kept version");
        kept.unshift(dec.decode(fs.get(p)!.data));
        fs.set(p, { dir: false, data: enc.encode(old), mtime: tick() });
        changed("KIND_CHANGED", p, false);
        return {};
      }

      case "CodeService/OpenWorkspace": {
        const sub = str("subscriptionId");
        if (!subs.some((s) => s.id === sub)) throw new RpcError("not_found", "the event subscription has ended; subscribe again");
        const root = abs(str("path"));
        if (!fs.has(root)) throw notFound(root);
        if (!fs.get(root)!.dir) throw new RpcError("invalid_argument", `${root} is not a folder`);
        const id = `ws_${++wsCount}`;
        workspaces[id] = { root, sub, open: true };
        // The watch's first word, sent before the reply as aosd may.
        subs.find((s) => s.id === sub)?.push({ event: { time: new Date().toISOString(), workspace: { workspaceId: id, watch: { state: "STATE_LIVE" } } } });
        return { id, root, rootMode: Boolean(opts.rootMode) };
      }
      case "CodeService/CloseWorkspace": {
        const w = workspaces[str("workspaceId")];
        if (!w?.open) throw new RpcError("not_found", "no such open Workspace");
        w.open = false;
        return {};
      }
      case "CodeService/ReportDirty": {
        const id = str("workspaceId");
        if (!workspaces[id]?.open) throw new RpcError("not_found", "no such open Workspace");
        machine.dirty[id] = (body.paths as string[]) ?? [];
        return {};
      }
    }
    return {};
  }

  // A server stream: Connect's envelopes over a body the fake keeps open.
  function stream(method: string, raw: Uint8Array, signal?: AbortSignal | null): Response {
    let ctrl!: ReadableStreamDefaultController<Uint8Array>;
    let done = false;
    const frame = (flags: number, msg: unknown) => {
      const json = enc.encode(JSON.stringify(msg));
      const out = new Uint8Array(5 + json.length);
      out[0] = flags;
      new DataView(out.buffer).setUint32(1, json.length);
      out.set(json, 5);
      return out;
    };
    const body = new ReadableStream<Uint8Array>({ start: (c) => void (ctrl = c) });
    const push = (msg: unknown) => !done && ctrl.enqueue(frame(0, msg));
    const end = () => {
      if (done) return;
      done = true;
      ctrl.enqueue(frame(2, {}));
      ctrl.close();
    };
    if (method === "EventService/Subscribe") {
      const sub: Sub = {
        id: `sub_${++subCount}`,
        push,
        end: () => {
          end();
          // aosd closes a subscription's Workspaces when it ends.
          for (const w of Object.values(workspaces)) if (w.sub === sub.id) w.open = false;
          subs.splice(subs.indexOf(sub), 1);
        },
      };
      subs.push(sub);
      machine.subscriptions.push(sub.id);
      push({ subscriptionId: sub.id });
      signal?.addEventListener("abort", () => sub.end());
    } else if (method === "FileService/Watch") {
      // Finder's folder stream: the listing now, then held open.
      const req = JSON.parse(dec.decode(raw.slice(5))) as Record<string, unknown>;
      queueMicrotask(() => {
        try {
          push(unary("FileService/List", req));
        } catch {
          end();
        }
      });
    } else {
      end();
    }
    return new Response(body, { status: 200, headers: { "Content-Type": "application/connect+json" } });
  }

  const realFetch = window.fetch.bind(window);
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, location.href);
    if (url.pathname === "/realm") return new Response(JSON.stringify({ rootMode: Boolean(opts.rootMode) }), { headers: { "Content-Type": "application/json" } });
    const m = url.pathname.match(/^\/aos\.v1\.(\w+\/\w+)$/);
    if (!m) return realFetch(input, init);
    const method = m[1];
    machine.calls.push(method);
    const contentType = new Headers(init?.headers).get("Content-Type") ?? "";
    const raw = new Uint8Array(await new Response(init?.body ?? null).arrayBuffer());
    if (contentType.includes("connect+json")) return stream(method, raw, init?.signal);
    await new Promise((r) => setTimeout(r, 5)); // a round trip, so ordering is not free
    try {
      const out = unary(method, raw.length ? (JSON.parse(dec.decode(raw)) as Record<string, unknown>) : {});
      return new Response(JSON.stringify(out), { status: 200, headers: { "Content-Type": "application/json" } });
    } catch (err) {
      if (!(err instanceof RpcError)) throw err;
      const status = { not_found: 404, already_exists: 409, permission_denied: 403, invalid_argument: 400, failed_precondition: 400 }[err.code] ?? 500;
      return new Response(JSON.stringify({ code: err.code, message: err.message, details: err.details }), { status, headers: { "Content-Type": "application/json" } });
    }
  };
}
