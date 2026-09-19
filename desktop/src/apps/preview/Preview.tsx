// Preview (PLAN.md §4.3): one file per window, opened from the Finder, Spotlight
// or an Agent's open_in_desktop. The file's path is kept with the window, so a
// reload reopens it.
import { useWinState } from "../../shell/win";
import { useDesktop } from "../../store";
import { fileKind } from "../filetypes";
import { download } from "../finder/fs";
import PreviewBody from "./PreviewBody";

export default function Preview() {
  const [path] = useWinState("path", "");
  const openApp = useDesktop((s) => s.openApp);
  const revealInFinder = useDesktop((s) => s.revealInFinder);

  if (!path) {
    return (
      <div className="preview preview--empty">
        <p className="preview__note">Open an image, a PDF, audio or a video from the Finder to see it here.</p>
      </div>
    );
  }
  const name = path.slice(path.lastIndexOf("/") + 1);
  const dir = path.slice(0, path.lastIndexOf("/")) || "/";
  return (
    <div className="preview">
      <div className="preview__bar">
        <span className="preview__path" title={path}>
          {path}
        </span>
        {fileKind(name) === "text" && (
          <button className="finder__btn" onClick={() => openApp("textedit", path)}>
            Open in TextEdit
          </button>
        )}
        <button className="finder__btn" title="Show in Finder" onClick={() => revealInFinder(dir, path)}>
          Show in Finder
        </button>
        <button className="finder__btn" title="Download to this computer" onClick={() => download(path, name)}>
          ⬇
        </button>
      </div>
      <div className="preview__body">
        <PreviewBody path={path} />
      </div>
    </div>
  );
}
