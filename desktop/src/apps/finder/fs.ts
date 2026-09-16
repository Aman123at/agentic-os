// Finder helpers: the sidebar places, and small formatters and file readers
// over FileService (PLAN.md §4.3, §13). Kept apart from the component so the
// Finder chunk stays readable.
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

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

// basename is the last path segment (a file or folder's own name).
export function basename(path: string): string {
  const clean = path.replace(/\/+$/, "");
  const i = clean.lastIndexOf("/");
  return i < 0 ? clean : clean.slice(i + 1);
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
  // "/shared" reads as "Shared"; other absolute paths keep a single "/" root.
  const out = [{ name: dir.startsWith("/shared") ? "Shared" : "/", path: dir.startsWith("/shared") ? "/shared" : "/" }];
  const rest = dir.startsWith("/shared") ? dir.slice("/shared".length) : dir;
  let at = out[0].path === "/" ? "" : "/shared";
  for (const seg of rest.split("/").filter(Boolean)) {
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
// Takes only the fields the icon depends on, so the Trash can ask for the icon
// of a file it no longer has a FileInfo for (PLAN.md M4.8 item 8.19).
export function iconFor(e: Pick<FileInfo, "name" | "dir" | "symlink">): string {
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

// rawUrl is where aosd serves a file's bytes (PLAN.md §13): Range requests for
// media, inline for types that cannot run script, and a download otherwise.
export function rawUrl(path: string, download = false): string {
  const q = new URLSearchParams({ path });
  if (download) q.set("download", "1");
  return `/files/raw?${q}`;
}

// downloadToHost saves a file to the user's own computer. The browser streams
// it from /files/raw, so its size is no concern.
export function downloadToHost(path: string, name: string): void {
  const a = document.createElement("a");
  a.href = rawUrl(path, true);
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

// UploadError is a refused upload; status 409 means the file already exists.
export class UploadError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

// uploadFile streams a file from this computer to path through /upload.
export async function uploadFile(path: string, file: Blob, overwrite: boolean): Promise<void> {
  const q = new URLSearchParams({ path });
  if (overwrite) q.set("overwrite", "1");
  const resp = await fetch(`/upload?${q}`, { method: "POST", body: file, credentials: "same-origin" });
  if (!resp.ok) throw new UploadError((await resp.text()).trim() || resp.statusText, resp.status);
}

// looksBinary is a quick NUL-byte check, so a preview shows "no preview" and
// TextEdit refuses instead of showing garbage for a binary file.
export function looksBinary(bytes: Uint8Array): boolean {
  const n = Math.min(bytes.length, 4000);
  for (let i = 0; i < n; i++) if (bytes[i] === 0) return true;
  return false;
}
