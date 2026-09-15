// Software's Replay view (PLAN.md §11): the progress of re-applying the Install
// Ledger at startup, which the menu bar also shows while it runs. The status
// arrives with Info at boot and stays current through ReplayProgress events, so
// this view needs no polling of its own.
import { ReplayState } from "../../gen/aos/v1/types_pb";
import { useDesktop } from "../../store";
import { when } from "../agent/format";
import { replayLabel } from "./replay";

export default function ReplayView() {
  const replay = useDesktop((s) => s.replay);

  if (!replay || replay.state === ReplayState.UNSPECIFIED) {
    return <div className="agent__empty">No Ledger replay has run this session.</div>;
  }

  const label = replayLabel(replay.state);
  const running = replay.state === ReplayState.RUNNING;
  const pct = replay.total > 0 ? Math.min((replay.done / replay.total) * 100, 100) : running ? 0 : 100;

  return (
    <div className="replay">
      <section className="replay__card">
        <div className="replay__head">
          <h3 className="replay__h">Ledger replay</h3>
          <span className={`replay__state replay__state--${label.mod}`}>{label.text}</span>
        </div>
        <div className="replay__bar" role="progressbar" aria-valuemin={0} aria-valuemax={replay.total || 1} aria-valuenow={replay.done}>
          <div className={`replay__fill replay__fill--${label.mod}`} style={{ width: `${pct}%` }} />
        </div>
        <p className="replay__count">
          {replay.done} of {replay.total} step{replay.total === 1 ? "" : "s"}
        </p>
        {replay.message && <p className="replay__message">{replay.message}</p>}
        <p className="replay__times">
          {replay.startedAt && <>Started {when(replay.startedAt, true)}</>}
          {replay.finishedAt && <> · finished {when(replay.finishedAt, true)}</>}
        </p>
        <p className="replay__note">
          At startup the Machine re-applies the Install Ledger so its software, /etc files and Services match what they were, on top of the base image.
        </p>
      </section>
    </div>
  );
}
