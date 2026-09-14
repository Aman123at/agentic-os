// Finder helpers: the sidebar places, and small formatters and file readers
// over FileService (PLAN.md §4.3, §13). Kept apart from the component so the
// Finder chunk stays readable.
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

import { files } from "../../api/client";
import type { FileInfo } from "../../gen/aos/v1/services_pb";

// A Place is a sidebar shortcut. `path` is the folder it opens; the Trash place
// is special (path "") and browses TrashService instead of FileService.
export interface Place {
  id: string;
  name: string;
  icon: string;
  path: string;
}

export const PLACES: Place[] = [
  { id: "home", name: "Home", icon: "🏠", path: "~" },
  { id: "downloads", name: "Downloads", icon: "⬇️", path: "~/Downloads" },
  { id: "shared", name: "Shared", icon: "🤝", path: "/shared" },
  { id: "trash", name: "Trash", icon: "🗑️", path: "" },
];

// join appends a name to a folder path, keeping "~" and absolute paths tidy.
export function join(dir: string, name: string): string {
  if (dir === "~") return `~/${name}`;
  return `${dir.replace(/\/+$/, "")}/${name}`;
}

// parent returns the containing folder, or "" at a root ("~", "/", "/shared").
export function parent(dir: string): string {
  if (dir === "~" || dir === "/" || dir === "/shared") return "";
  if (dir.startsWith("~/")) {
    const up = dir.slice(0, dir.lastIndexOf("/"));
    return up === "~" ? "~" : up;
  }
  const up = dir.slice(0, dir.lastIndexOf("/"));
  return up === "" ? "/" : up;
}

// crumbs turns a path into its clickable segments for the toolbar.
export function crumbs(dir: string): Array<{ name: string; path: string }> {
  if (dir === "~") return [{ name: "Home", path: "~" }];
  if (dir.startsWith("~/")) {
    const out = [{ name: "Home", path: "~" }];
    let at = "~";
    for (const seg of dir.slice(2).split("/")) {
      at = `${at}/${seg}`;
      out.push({ name: seg, path: at });
    }
    return out;
  }
  const out = [{ name: "/", path: "/" }];
  let at = "";
  for (const seg of dir.split("/").filter(Boolean)) {
    at = `${at}/${seg}`;
    out.push({ name: seg, path: at });
  }
  return out;
}

export function formatSize(bytes: bigint, dir: boolean): string {
  if (dir) return "—";
  const n = Number(bytes);
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

export function formatWhen(ts?: Timestamp): string {
  if (!ts) return "—";
  return timestampDate(ts).toLocaleDateString([], { year: "numeric", month: "short", day: "numeric" });
}

// iconFor picks an emoji for an entry from its kind and extension. The drawn SVG
// icon set replaces these in M3.5.
export function iconFor(e: FileInfo): string {
  if (e.dir) return "📁";
  if (e.symlink) return "🔗";
  const ext = e.name.slice(e.name.lastIndexOf(".") + 1).toLowerCase();
  if (isImageExt(ext)) return "🖼️";
  if (["md", "txt", "rtf", "log"].includes(ext)) return "📄";
  if (["go", "ts", "tsx", "js", "jsx", "py", "rs", "c", "h", "sh", "json", "yaml", "yml", "toml"].includes(ext)) return "📝";
  if (["zip", "tar", "gz", "tgz", "bz2", "xz"].includes(ext)) return "🗜️";
  if (["pdf"].includes(ext)) return "📕";
  return "📄";
}

const IMAGE_EXTS = ["png", "jpg", "jpeg", "gif", "webp", "bmp", "svg", "ico", "avif"];
export function isImageExt(ext: string): boolean {
  return IMAGE_EXTS.includes(ext);
}
export function isImage(e: FileInfo): boolean {
  return !e.dir && isImageExt(e.name.slice(e.name.lastIndexOf(".") + 1).toLowerCase());
}

const MAX_PREVIEW = 4 << 20; // stop pulling a file for Quick Look past 4 MiB

// readAll pulls a whole file in 1 MiB chunks (FileService.Read caps each call),
// up to max bytes. Used for Quick Look and download-to-Host.
export async function readAll(path: string, max = MAX_PREVIEW): Promise<Uint8Array> {
  const parts: Uint8Array[] = [];
  let offset = 0n;
  for (;;) {
    const resp = await files.read({ path, offset, limit: 0n });
    parts.push(resp.content);
    offset += BigInt(resp.content.length);
    if (resp.eof || offset >= BigInt(max) || resp.content.length === 0) break;
  }
  let total = 0;
  for (const p of parts) total += p.length;
  const out = new Uint8Array(total);
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

const decoder = new TextDecoder("utf-8", { fatal: false });
export function decodeText(bytes: Uint8Array): string {
  return decoder.decode(bytes);
}

// looksBinary is a quick NUL-byte check so Quick Look shows "no preview" instead
// of garbage for binaries it does not recognise.
export function looksBinary(bytes: Uint8Array): boolean {
  const n = Math.min(bytes.length, 4000);
  for (let i = 0; i < n; i++) if (bytes[i] === 0) return true;
  return false;
}
