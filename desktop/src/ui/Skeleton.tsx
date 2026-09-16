// The shape of content that has not arrived yet. A window used to show the word
// "Loading…" in the middle of an empty pane while its chunk and its first
// listing were on the way, which reads as a stall; a few shimmering rows read as
// the window filling in (PLAN.md §4.3, M4.8 item 8.18).
export function Skeleton({ rows = 8 }: { rows?: number }) {
  return (
    <div className="skeleton" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="skeleton__row" style={{ width: `${88 - ((i * 13) % 42)}%` }} />
      ))}
    </div>
  );
}
