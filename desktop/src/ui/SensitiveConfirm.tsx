// The ask before changing a Sensitive System File as root (PLAN.md M8.5): red,
// naming the file and what can go wrong, and saying AOS can't undo it. When
// the file has a check (visudo, sshd -t, findmnt --verify) its result is shown
// first, and a failed check turns the button into "Save anyway (check failed)".
import { useEffect, useRef } from "react";

import type { CheckSensitiveResponse } from "../gen/aos/v1/services_pb";

export interface SensitiveAsk {
  path: string;
  reason: string;
  // What is about to happen: "Save", "Move to Trash", "Rename"…
  action: string;
  // The button, when "<action> anyway" doesn't read well.
  confirmLabel?: string;
  // The check's result; "checking" while it runs; undefined when none ran.
  check?: CheckSensitiveResponse | "checking";
  // Whether a version is kept to restore: saves keep one, moves and deletes
  // go to the Trash or stay where they land.
  keeps: boolean;
}

export function SensitiveConfirm({ ask, onConfirm, onCancel }: { ask: SensitiveAsk; onConfirm: () => void; onCancel: () => void }) {
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => cancel.current?.focus(), []);
  const check = ask.check;
  const failed = check !== undefined && check !== "checking" && check.checker !== "" && !check.passed && !check.unavailable;
  const title = `${ask.action} ${ask.path}?`;
  return (
    <div
      className="confirm__scrim"
      onClick={onCancel}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onCancel();
        }
      }}
    >
      <div className="confirm sensitive" role="alertdialog" aria-label={title} aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h3 className="confirm__title sensitive__title">⚠︎ {title}</h3>
        <p className="confirm__message sensitive__message">
          <strong>{ask.path} is a Sensitive System File.</strong> {ask.reason} You are root in Root Mode, and AOS can’t undo this
          {ask.keeps ? " — but it keeps the version you replace, and Restore previous version puts it back." : "."}
        </p>
        {check === "checking" && <p className="sensitive__check">Checking…</p>}
        {check !== undefined && check !== "checking" && check.checker !== "" && (
          <div className={`sensitive__check${failed ? " sensitive__check--failed" : ""}`} role="status">
            {check.unavailable ? (
              <span>
                {check.checker.split(" ")[0]} isn’t installed, so this can’t be checked with {check.checker}.
              </span>
            ) : check.passed ? (
              <span>✓ {check.checker} found no problems.</span>
            ) : (
              <span>✗ {check.checker} found problems:</span>
            )}
            {check.output && !check.passed && <pre className="sensitive__output">{check.output}</pre>}
          </div>
        )}
        <div className="confirm__actions">
          <button ref={cancel} className="finder__btn" onClick={onCancel}>
            Cancel
          </button>
          <button className="finder__btn finder__btn--on confirm__danger" disabled={check === "checking"} onClick={onConfirm}>
            {ask.confirmLabel ?? `${ask.action} anyway`}
            {failed ? " (check failed)" : ""}
          </button>
        </div>
      </div>
    </div>
  );
}
