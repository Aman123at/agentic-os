// Code (PLAN.md M8): a folder of the Machine in an editor. One Workspace per
// window; the window asks for a folder when opened from the Dock, and Finder's
// Open in Code and Spotlight open one directly. Everything it reads and writes
// goes through aosd as the Realm's user, like Finder and the Terminal.
import { useContext, useState } from "react";

import { WinContext, useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { Workspace } from "./Workspace";
import { baseOf } from "./paths";

export default function Code() {
  const [path, setPath] = useWinState("path", "");
  if (!path) return <OpenFolder onOpen={setPath} />;
  return <Workspace key={path} path={path} onClose={() => setPath("")} />;
}

// OpenFolder asks which folder to open; the home folder unless told otherwise.
function OpenFolder({ onOpen }: { onOpen: (path: string) => void }) {
  const winId = useContext(WinContext);
  const setWinTitle = useDesktop((s) => s.setWinTitle);
  const rootMode = useDesktop((s) => Boolean(s.info?.rootMode));
  const [value, setValue] = useState("~");
  return (
    <form
      className="code-open"
      onSubmit={(e) => {
        e.preventDefault();
        const p = value.trim();
        if (!p) return;
        setWinTitle(winId, baseOf(p));
        onOpen(p);
      }}
    >
      <div className="code-open__card">
        <h2 className="code-open__title">Open a folder</h2>
        <p className="code-open__note">
          Code reads and writes as {rootMode ? <strong>root</strong> : <strong>aos</strong>}, in {rootMode ? "Root Mode" : "Standard Mode"}.
        </p>
        <label className="code-open__field">
          Folder
          <input className="textedit__input" aria-label="Folder to open" autoFocus value={value} spellCheck={false} onChange={(e) => setValue(e.target.value)} />
        </label>
        <button className="finder__btn finder__btn--on" type="submit">
          Open
        </button>
      </div>
    </form>
  );
}
