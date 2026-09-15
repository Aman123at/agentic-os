// A PDF, one page at a time, rendered by pdf.js (PLAN.md §4.3). pdf.js and its
// worker are their own lazy chunks, fetched the first time a PDF opens. It asks
// /files/raw for the ranges it needs, so a large document's first page shows
// before the rest has downloaded.
import * as pdfjs from "pdfjs-dist";
import type { PDFDocumentProxy, RenderTask } from "pdfjs-dist";
import workerUrl from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { useEffect, useRef, useState } from "react";

pdfjs.GlobalWorkerOptions.workerSrc = workerUrl;

const ZOOMS = [0.5, 0.75, 1, 1.25, 1.5, 2, 3];

export default function PdfView({ url, compact }: { url: string; compact: boolean }) {
  const [doc, setDoc] = useState<PDFDocumentProxy>();
  const [error, setError] = useState("");
  const [page, setPage] = useState(1);
  const [zoom, setZoom] = useState(2); // an index into ZOOMS; 100% fits the width
  const [width, setWidth] = useState(0);
  const box = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    setDoc(undefined);
    setError("");
    setPage(1);
    const task = pdfjs.getDocument({ url, withCredentials: true, disableAutoFetch: true, disableStream: true, rangeChunkSize: 1 << 20 });
    task.promise.then(setDoc, (err: Error) => setError(err.message));
    return () => void task.destroy();
  }, [url]);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    if (!doc || !width || !canvas.current) return;
    let render: RenderTask | undefined;
    let gone = false;
    void doc.getPage(page).then((p) => {
      if (gone || !canvas.current) return;
      const fit = Math.max(width - 32, 100) / p.getViewport({ scale: 1 }).width;
      const dpr = window.devicePixelRatio || 1;
      const scale = fit * ZOOMS[zoom];
      const viewport = p.getViewport({ scale: scale * dpr });
      const c = canvas.current;
      c.width = Math.floor(viewport.width);
      c.height = Math.floor(viewport.height);
      c.style.width = `${Math.floor(viewport.width / dpr)}px`;
      c.style.height = `${Math.floor(viewport.height / dpr)}px`;
      render = p.render({ canvas: c, viewport });
      render.promise.catch((err: Error) => {
        if (err.name !== "RenderingCancelledException") setError(err.message);
      });
    });
    return () => {
      gone = true;
      render?.cancel();
    };
  }, [doc, page, zoom, width]);

  const pages = doc?.numPages ?? 0;
  return (
    <div className={`pdf${compact ? " pdf--compact" : ""}`}>
      <div className="pdf__bar">
        <button className="finder__btn" title="Previous page" disabled={page <= 1} onClick={() => setPage(page - 1)}>
          ‹
        </button>
        <span className="pdf__page">{pages ? `Page ${page} of ${pages}` : "…"}</span>
        <button className="finder__btn" title="Next page" disabled={page >= pages} onClick={() => setPage(page + 1)}>
          ›
        </button>
        {!compact && (
          <>
            <span className="pdf__gap" />
            <button className="finder__btn" title="Zoom out" disabled={zoom === 0} onClick={() => setZoom(zoom - 1)}>
              −
            </button>
            <span className="pdf__zoom">{Math.round(ZOOMS[zoom] * 100)}%</span>
            <button className="finder__btn" title="Zoom in" disabled={zoom === ZOOMS.length - 1} onClick={() => setZoom(zoom + 1)}>
              +
            </button>
          </>
        )}
      </div>
      <div className="pdf__scroll" ref={box}>
        {error ? <div className="preview__note">This PDF could not be shown: {error}</div> : <canvas ref={canvas} className="pdf__canvas" />}
      </div>
    </div>
  );
}
