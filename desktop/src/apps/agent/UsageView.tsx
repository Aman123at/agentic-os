// The Agent app's Usage view (PLAN.md §8.4): today's model tokens and estimated
// spend, the last 30 days as bars from SystemService.Usage, and the Cost Limits
// in force. It reloads when a Task's usage changes while it is open.
import { useCallback, useEffect, useState } from "react";

import { system } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { DailyUsage, InfoResponse } from "../../gen/aos/v1/services_pb";
import type { Usage } from "../../gen/aos/v1/types_pb";
import { useDesktop } from "../../store";
import { costLine } from "./format";

const DAYS = 30;

export default function UsageView() {
  const tasks = useDesktop((s) => s.tasks);
  const [days, setDays] = useState<DailyUsage[]>([]);
  const [info, setInfo] = useState<InfoResponse>();
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    try {
      const [u, i] = await Promise.all([system.usage({ days: DAYS }), system.info({})]);
      setDays(u.days);
      setInfo(i);
      setError("");
    } catch (err) {
      setError(friendlyError(err));
    }
  }, []);

  // Tasks change as they run; reload at most once a second while they do.
  useEffect(() => {
    const t = setTimeout(() => void refresh(), days.length ? 1000 : 0);
    return () => clearTimeout(t);
  }, [tasks, refresh]);

  // Bars show spend when every day's cost is known, and tokens otherwise.
  const byCost = days.every((d) => !d.usage || d.usage.costKnown || tokens(d.usage) === 0);
  const values = days.map((d) => (byCost ? (d.usage?.costUsd ?? 0) : tokens(d.usage)));
  const max = Math.max(...values, 0);
  const today = info?.today;
  const daily = info?.dailyCostLimitUsd ?? 0;
  const perTask = info?.taskCostLimitUsd ?? 0;

  return (
    <div className="usage">
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}

      <section className="usage__card">
        <h3 className="usage__h">Today</h3>
        <p className="usage__big">{today?.costKnown ? `$${today.costUsd.toFixed(4)}` : today && tokens(today) ? "Cost unknown" : "$0.0000"}</p>
        <p className="usage__line">{today ? costLine(today) : "…"}</p>
        {info && !info.pricesKnown && <p className="usage__note">There are no prices for {info.model}, so its cost is not estimated.</p>}
      </section>

      <section className="usage__card">
        <h3 className="usage__h">Last {DAYS} days</h3>
        <p className="usage__line">{byCost ? "Estimated spend per day" : "Tokens per day"}</p>
        <div className="usage__chart" role="img" aria-label={`Model usage over the last ${DAYS} days`}>
          {days.map((d, i) => (
            <div key={d.day} className="usage__col" title={`${d.day}: ${d.usage ? costLine(d.usage) : "no usage"}`}>
              <div className="usage__bar" style={{ height: `${max > 0 ? Math.max((values[i] / max) * 100, values[i] > 0 ? 2 : 0) : 0}%` }} />
            </div>
          ))}
        </div>
        <div className="usage__axis">
          <span>{days[0]?.day ?? ""}</span>
          <span>{days.at(-1)?.day ?? ""}</span>
        </div>
      </section>

      <section className="usage__card">
        <h3 className="usage__h">Cost Limits</h3>
        <p className="usage__line">
          Per Task: <strong>{perTask > 0 ? `$${perTask.toFixed(2)}` : "no limit"}</strong>
        </p>
        <p className="usage__line">
          Per day: <strong>{daily > 0 ? `$${daily.toFixed(2)}` : "no limit"}</strong>
          {daily > 0 && today?.costKnown && ` · $${today.costUsd.toFixed(4)} used today`}
        </p>
        {daily > 0 && today?.costKnown && (
          <div className="usage__meter" role="meter" aria-label="Today's spend against the daily limit" aria-valuemin={0} aria-valuemax={daily} aria-valuenow={today.costUsd}>
            <div className="usage__meterfill" style={{ width: `${Math.min((today.costUsd / daily) * 100, 100)}%` }} />
          </div>
        )}
        <p className="usage__note">A Task that reaches a limit stops and asks you before it spends more. Change the limits in System Settings.</p>
      </section>
    </div>
  );
}

function tokens(u?: Usage): number {
  return u ? Number(u.inputTokens) + Number(u.outputTokens) : 0;
}
