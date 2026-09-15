// Confirm asks before an action that cannot be undone, inside the window that
// asked, instead of the browser's blocking confirm(). Enter confirms and Escape
// cancels; the safe choice has the focus.
import { useEffect, useRef } from "react";

export interface ConfirmProps {
  title: string;
  message: string;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function Confirm({ title, message, confirmLabel, danger, onConfirm, onCancel }: ConfirmProps) {
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => cancel.current?.focus(), []);
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
      <div className="confirm" role="alertdialog" aria-label={title} aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h3 className="confirm__title">{title}</h3>
        <p className="confirm__message">{message}</p>
        <div className="confirm__actions">
          <button ref={cancel} className="finder__btn" onClick={onCancel}>
            Cancel
          </button>
          <button className={`finder__btn finder__btn--on${danger ? " confirm__danger" : ""}`} onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
