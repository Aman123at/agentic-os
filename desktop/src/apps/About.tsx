import { useDesktop } from "../store";

// About This Machine shows what SystemService.Info reports (PLAN.md §13).
export default function About() {
  const info = useDesktop((s) => s.info);
  return (
    <div className="about">
      <div className="about__logo">🖥️</div>
      <h2 className="about__name">Agentic OS</h2>
      <dl className="about__facts">
        <dt>Mode</dt>
        <dd>{info?.mode ?? "—"}</dd>
        <dt>Version</dt>
        <dd>{info?.version || "dev"}</dd>
        <dt>Model</dt>
        <dd>{info?.model ?? "—"}</dd>
        <dt>Landlock ABI</dt>
        <dd>{info?.landlockAbi ?? 0}</dd>
      </dl>
    </div>
  );
}
