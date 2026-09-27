// Reading and describing a file for an editor (PLAN.md M8.3): Code opens text
// files up to 4 MiB, read through FileService in chunks, as the Realm's user.
import { files } from "../api/client";
import type { FileInfo } from "../gen/aos/v1/services_pb";
import { looksBinary } from "../apps/finder/fs";

// MAX_TEXT is the largest file Code opens or saves.
export const MAX_TEXT = 4 << 20;
// FileService.Read returns at most this much at once.
const CHUNK = 1 << 20;

// stamp identifies a version of a file on disk: two stats with the same stamp
// saw the same version.
export function stamp(info?: FileInfo): string {
  return info ? `${info.size}:${info.modifiedAt?.seconds ?? 0}:${info.modifiedAt?.nanos ?? 0}` : "";
}

export type Loaded = { kind: "text"; text: string; info: FileInfo } | { kind: "binary"; info: FileInfo } | { kind: "too-big"; info: FileInfo };

// readText reads a file for editing: its text, or why it can't be edited.
export async function readText(path: string, signal?: AbortSignal): Promise<Loaded> {
  const info = (await files.stat({ path }, { signal })).info;
  if (!info) throw new Error(`There is nothing at ${path}.`);
  if (info.dir) throw new Error(`${path} is a folder.`);
  if (info.size > BigInt(MAX_TEXT)) return { kind: "too-big", info };
  const parts: Uint8Array[] = [];
  let offset = 0n;
  for (;;) {
    const r = await files.read({ path, offset, limit: BigInt(CHUNK) }, { signal });
    parts.push(r.content);
    offset += BigInt(r.content.length);
    if (r.eof || r.content.length === 0 || offset > BigInt(MAX_TEXT)) break;
  }
  const bytes = concat(parts);
  if (bytes.length > MAX_TEXT) return { kind: "too-big", info };
  if (looksBinary(bytes)) return { kind: "binary", info };
  try {
    return { kind: "text", text: new TextDecoder("utf-8", { fatal: true }).decode(bytes), info };
  } catch {
    return { kind: "binary", info };
  }
}

function concat(parts: Uint8Array[]): Uint8Array {
  if (parts.length === 1) return parts[0];
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

// Indent is how a file indents: with tabs, or with some number of spaces.
export interface Indent {
  tabs: boolean;
  size: number;
}

// detectIndent guesses a file's indentation from its first indented lines: tabs
// if most start with one, else the most common step between space indents.
// A file with none gets 2 spaces, or tabs for Go and Makefiles.
export function detectIndent(text: string, name: string): Indent {
  const lower = name.toLowerCase();
  const fallback: Indent = lower.endsWith(".go") || lower.endsWith("makefile") ? { tabs: true, size: 4 } : { tabs: false, size: 2 };
  let tabs = 0;
  let spaces = 0;
  const steps = new Map<number, number>();
  let prev = 0;
  let seen = 0;
  for (const line of text.split("\n", 2000)) {
    if (!line.trim()) continue;
    if (line[0] === "\t") {
      tabs++;
      seen++;
    } else if (line[0] === " ") {
      const n = line.length - line.trimStart().length;
      const step = Math.abs(n - prev);
      if (step > 0 && step <= 8) steps.set(step, (steps.get(step) ?? 0) + 1);
      prev = n;
      spaces++;
      seen++;
      continue;
    }
    prev = 0;
    if (seen >= 200) break;
  }
  if (tabs === 0 && spaces === 0) return fallback;
  if (tabs > spaces) return { tabs: true, size: 4 };
  let best = 0;
  let bestCount = 0;
  for (const [step, count] of steps) {
    if (count > bestCount || (count === bestCount && step < best)) {
      best = step;
      bestCount = count;
    }
  }
  return { tabs: false, size: best || fallback.size };
}
