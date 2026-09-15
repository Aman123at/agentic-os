// A virtualised list (PLAN.md §4.3 rule 5): only the rows in view, plus a few
// either side, are rendered, so a folder of thousands of entries, the process
// table or the Audit Log scrolls like a short one. Rows have a fixed height and
// sit absolutely inside a spacer as tall as the whole list; scrolling renders
// again only when the first row in view changes.
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";

const OVERSCAN = 6; // rows rendered beyond each edge, so fast scrolling shows no gap

export interface VirtualListProps<T> {
  items: readonly T[];
  rowHeight: number;
  className?: string;
  // Rendered above the rows; style it `position: sticky; top: 0` to keep it in view.
  header?: ReactNode;
  // Returns one keyed row; spread `style` onto it, since it places the row.
  renderRow: (item: T, style: CSSProperties, index: number) => ReactNode;
  // Called when scrolling nears the end, so a paged list can fetch the next page.
  onEndReached?: () => void;
}

export function VirtualList<T>({ items, rowHeight, className, header, renderRow, onEndReached }: VirtualListProps<T>) {
  const box = useRef<HTMLDivElement>(null);
  const [first, setFirst] = useState(0);
  const [height, setHeight] = useState(300);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    setHeight(el.clientHeight);
    const ro = new ResizeObserver(() => setHeight(el.clientHeight));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const count = Math.ceil(height / rowHeight) + 2 * OVERSCAN;
  // A list that got shorter while scrolled (a live refresh) still shows its end.
  const start = Math.max(0, Math.min(first, items.length - count));

  return (
    <div
      className={className}
      ref={box}
      onScroll={(e) => {
        const el = e.currentTarget;
        setFirst(Math.max(0, Math.floor(el.scrollTop / rowHeight) - OVERSCAN));
        if (onEndReached && el.scrollTop + el.clientHeight >= el.scrollHeight - OVERSCAN * rowHeight) onEndReached();
      }}
    >
      {header}
      <div style={{ height: items.length * rowHeight, position: "relative" }}>
        {items
          .slice(start, start + count)
          .map((item, i) =>
            renderRow(item, { position: "absolute", left: 0, right: 0, top: (start + i) * rowHeight, height: rowHeight }, start + i),
          )}
      </div>
    </div>
  );
}
