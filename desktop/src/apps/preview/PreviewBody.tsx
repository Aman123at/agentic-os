// A file's preview, shared by the Preview app and the Finder's Quick Look. Bytes
// come from /files/raw (PLAN.md §13): images and media load straight from it,
// with Range requests for media; text shows its first part; a PDF renders in
// pdf.js, fetched only when one is opened. A header-less element load (an <img>,
// a <video>) carries a single-use ticket in its URL; a text peek fetches with
// the access token in a header instead (ADR-0007, M6.5).
import { lazy, Suspense, useEffect, useState } from "react";

import { authFetch } from "../../api/session";
import { fileKind } from "../filetypes";
import { looksBinary, rawUrl, ticketedRawUrl } from "../finder/fs";

const PdfView = lazy(() => import("./PdfView"));

// How much of a text file a preview shows.
const TEXT_BYTES = 256 << 10;

export default function PreviewBody({ path, compact = false }: { path: string; compact?: boolean }) {
  const name = path.slice(path.lastIndexOf("/") + 1);
  const kind = fileKind(name);
  if (kind === "image" || kind === "video" || kind === "audio" || kind === "pdf") {
    return <MediaPeek path={path} name={name} kind={kind} compact={compact} />;
  }
  return <TextPeek path={path} />;
}

// MediaPeek loads bytes through a header-less element, so it mints a single-use
// ticket for the load and puts it in the URL. A new ticket is minted per file.
function MediaPeek({ path, name, kind, compact }: { path: string; name: string; kind: string; compact: boolean }) {
  const [url, setUrl] = useState<string>();
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    setUrl(undefined);
    setFailed(false);
    ticketedRawUrl(path)
      .then((u) => live && setUrl(u))
      .catch(() => live && setFailed(true));
    return () => {
      live = false;
    };
  }, [path]);

  if (failed) return <div className="preview__note">{name} could not be loaded.</div>;
  if (url === undefined) return <div className="preview__note">Loading…</div>;
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
    default:
      return (
        <Suspense fallback={<div className="preview__note">Loading…</div>}>
          <PdfView url={url} compact={compact} />
        </Suspense>
      );
  }
}

function TextPeek({ path }: { path: string }) {
  const [state, setState] = useState<{ text?: string; truncated?: boolean; error?: string }>({});
  useEffect(() => {
    const abort = new AbortController();
    setState({});
    (async () => {
      const resp = await authFetch(rawUrl(path), { headers: { Range: `bytes=0-${TEXT_BYTES - 1}` }, signal: abort.signal });
      if (!resp.ok) throw new Error((await resp.text()).trim() || resp.statusText);
      const bytes = new Uint8Array(await resp.arrayBuffer());
      if (looksBinary(bytes)) return setState({ error: "No preview available." });
      const total = Number(resp.headers.get("Content-Range")?.split("/")[1] ?? bytes.length);
      setState({ text: new TextDecoder().decode(bytes), truncated: total > bytes.length });
    })().catch((err: Error) => !abort.signal.aborted && setState({ error: err.message }));
    return () => abort.abort();
  }, [path]);

  if (state.error) return <div className="preview__note">{state.error}</div>;
  if (state.text === undefined) return <div className="preview__note">Loading…</div>;
  return (
    <pre className="quicklook__text preview__text">
      {state.text}
      {state.truncated && "\n…"}
    </pre>
  );
}
