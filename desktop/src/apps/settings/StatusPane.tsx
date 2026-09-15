// The Status pane (PLAN.md §13, M4.5): what SystemService.Info reports about the
// running Machine — its Mode, the Landlock ABI the kernel offers, and versions.
// It is read-only; the panes that change things are the others.
import { useDesktop } from "../../store";

export default function StatusPane() {
  const info = useDesktop((s) => s.info);
  const landlock = info?.landlockAbi ?? 0;

  return (
    <div className="set__pane">
      <h2 className="set__title">Status</h2>

      <section className="set__group">
        <dl className="status__facts">
          <dt>Mode</dt>
          <dd>{info?.mode || "—"}</dd>
          <dt>Version</dt>
          <dd>{info?.version || "dev"}</dd>
          <dt>Model</dt>
          <dd>{info?.model || "—"}</dd>
          <dt>Landlock ABI</dt>
          <dd>{landlock > 0 ? `v${landlock}` : "not available"}</dd>
          <dt>File protection</dt>
          <dd>{landlock > 0 ? "Enforced by the kernel" : "Policy checks only"}</dd>
          <dt>Prices</dt>
          <dd>{info?.pricesKnown ? "Known for this model" : "Unknown for this model"}</dd>
        </dl>
      </section>
    </div>
  );
}
