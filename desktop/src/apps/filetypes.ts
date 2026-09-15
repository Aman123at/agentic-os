// Which Desktop app opens a file (PLAN.md §4.3): Preview shows images, PDFs,
// audio and video; TextEdit edits text; anything else (an archive, a program)
// is shown selected in the Finder. Decided by the name alone, so the store can
// route a file without reading it; TextEdit refuses one that turns out to be
// binary.

export type FileKind = "image" | "pdf" | "video" | "audio" | "text" | "other";

const KINDS: Record<string, FileKind> = {};
const add = (kind: FileKind, exts: string) => exts.split(" ").forEach((e) => (KINDS[e] = kind));
add("image", "png jpg jpeg gif webp avif bmp ico svg");
add("pdf", "pdf");
add("video", "mp4 m4v webm ogv mov");
add("audio", "mp3 m4a aac wav ogg oga opus flac");
add(
  "other",
  "zip tar gz tgz bz2 xz zst 7z rar jar war deb rpm apk dmg iso img exe dll so dylib o a class pyc wasm bin dat db sqlite woff woff2 ttf otf eot psd",
);

export function extOf(name: string): string {
  const base = name.slice(name.lastIndexOf("/") + 1);
  const dot = base.lastIndexOf(".");
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : "";
}

// fileKind reads a name's extension. Names with no extension, or one not listed
// (a Makefile, notes.md, main.go), are taken to be text.
export function fileKind(name: string): FileKind {
  return KINDS[extOf(name)] ?? "text";
}

export function appForFile(name: string): "preview" | "textedit" | null {
  const kind = fileKind(name);
  if (kind === "text") return "textedit";
  return kind === "other" ? null : "preview";
}
