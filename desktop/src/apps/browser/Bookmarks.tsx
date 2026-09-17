// The Browser's bookmarks (PLAN.md M5.2): the pages kept beside the page
// itself. A bookmark is an address and the page's title when it was starred.
// The list is kept with the window, like the address, so each Browser window
// has its own and a reload brings it back.
import { useEffect, useState } from "react";

import { ContextMenu, MenuItem } from "../../ui/ContextMenu";

export interface Bookmark {
  url: string;
  title: string;
}

// readMarks parses the list out of the window's state, which is a string. A
// layout written by an older build, or a hand-edited one, reads as no
// bookmarks rather than breaking the window.
export function readMarks(json: string): Bookmark[] {
  try {
    const v: unknown = JSON.parse(json);
    if (!Array.isArray(v)) return [];
    return v.filter((b): b is Bookmark => Boolean(b) && typeof (b as Bookmark).url === "string").map((b) => ({ url: b.url, title: typeof b.title === "string" ? b.title : "" }));
  } catch {
    return [];
  }
}

// host is the line under a bookmark's title: the address without its scheme.
function host(url: string): string {
  try {
    const u = new URL(url);
    return u.host + (u.pathname === "/" ? "" : u.pathname);
  } catch {
    return url;
  }
}

export default function Bookmarks({
  marks,
  current,
  onOpen,
  onRemove,
}: {
  marks: Bookmark[];
  // The address showing now, so the row for it reads as the current page.
  current: string;
  onOpen: (url: string) => void;
  onRemove: (url: string) => void;
}) {
  const [menu, setMenu] = useState<{ x: number; y: number; url: string } | null>(null);

  // A click anywhere, or losing focus, dismisses the row menu — as in the
  // Tasks list and the Finder.
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("blur", close);
    };
  }, [menu]);

  return (
    <>
      <p className="browser__markshead">Bookmarks</p>
      {marks.length === 0 ? (
        <p className="browser__marksempty">
          No bookmarks yet. Press ☆ to keep the page you are on.
        </p>
      ) : (
        <ul className="browser__marklist">
          {marks.map((b) => (
            <li key={b.url}>
              <button
                className={`browser__mark${b.url === current ? " browser__mark--on" : ""}`}
                aria-current={b.url === current ? "page" : undefined}
                title={b.url}
                onClick={() => onOpen(b.url)}
                onContextMenu={(e) => {
                  e.preventDefault();
                  setMenu({ x: e.clientX, y: e.clientY, url: b.url });
                }}
              >
                <span className="browser__marktitle">{b.title || host(b.url)}</span>
                <span className="browser__markhost">{host(b.url)}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {menu && (
        <ContextMenu x={menu.x} y={menu.y}>
          <MenuItem label="Open" onClick={() => onOpen(menu.url)} />
          <MenuItem label="Remove Bookmark" danger onClick={() => onRemove(menu.url)} />
        </ContextMenu>
      )}
    </>
  );
}
