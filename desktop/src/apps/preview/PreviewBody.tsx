// A file's preview, shared by the Preview app and the Finder's Quick Look. Bytes
// come from /files/raw (PLAN.md §13): images and media load straight from it,
// with Range requests for media; text shows its first part; a PDF renders in
// pdf.js, fetched only when one is opened.
import { lazy, Suspense, useEffect, useState } from "react";

import { fileKind } from "../filetypes";
import { looksBinary, rawUrl } from "../finder/fs";

const PdfView = lazy(() => import("./PdfView"));

// How much of a text file a preview shows.
const TEXT_BYTES = 256 << 10;

export default function PreviewBody({ path, compact = false }: { path: string; compact?: boolean }) {
  const name = path.slice(path.lastIndexOf("/") + 1);
  const kind = fileKind(name);
  const url = rawUrl(path);
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [path]);

  if (failed) return <div className="preview__note">{name} could not be loaded.</div>;
  switch (kind) {
    case "image":
      return (
        <div className="preview__stage">
          <img className="preview__image" src={url} alt={name} onError={() => setFailed(true)} draggable={false} />
        </div>
      );
    case "video":
      return (
        <div className="preview__stage">
          <video className="preview__video" src={url} controls preload="metadata" onError={() => setFailed(true)} />
        </div>
      );
    case "audio":
      return (
        <div className="preview__stage">
          <audio className="preview__audio" src={url} controls preload="metadata" onError={() => setFailed(true)} />
        </div>
      );
    case "pdf":
      return (
        <Suspense fallback={<div className="preview__note">Loading…</div>}>
          <PdfView url={url} compact={compact} />
        </Suspense>
      );
    default:
      return <TextPeek url={url} />;
  }
}

function TextPeek({ url }: { url: string }) {
  const [state, setState] = useState<{ text?: string; truncated?: boolean; error?: string }>({});
  useEffect(() => {
    const abort = new AbortController();
    setState({});
    (async () => {
      const resp = await fetch(url, { headers: { Range: `bytes=0-${TEXT_BYTES - 1}` }, credentials: "same-origin", signal: abort.signal });
      if (!resp.ok) throw new Error((await resp.text()).trim() || resp.statusText);
      const bytes = new Uint8Array(await resp.arrayBuffer());
      if (looksBinary(bytes)) return setState({ error: "No preview available." });
      const total = Number(resp.headers.get("Content-Range")?.split("/")[1] ?? bytes.length);
      setState({ text: new TextDecoder().decode(bytes), truncated: total > bytes.length });
    })().catch((err: Error) => !abort.signal.aborted && setState({ error: err.message }));
    return () => abort.abort();
  }, [url]);

  if (state.error) return <div className="preview__note">{state.error}</div>;
  if (state.text === undefined) return <div className="preview__note">Loading…</div>;
  return (
    <pre className="quicklook__text preview__text">
      {state.text}
      {state.truncated && "\n…"}
    </pre>
  );
}
