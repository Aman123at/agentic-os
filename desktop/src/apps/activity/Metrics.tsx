// Activity Monitor's CPU / Memory / Disk / Network tab (PLAN.md §4.3, M4.4):
// SystemService.Metrics polled every 2 s while the tab is open, each measure a
// sparkline over the recent samples. Network shows a rate worked out from the
// difference between successive cumulative counters.
import { useRef, useState } from "react";

import { system } from "../../api/client";
import { friendlyError } from "../../api/error";
import type { DiskUsage, MetricsResponse } from "../../gen/aos/v1/services_pb";
import { bytes, percent, rate } from "./format";
import { Sparkline } from "./Sparkline";
import { usePoll } from "./usePoll";

const HISTORY = 60; // samples kept, i.e. two minutes at 2 s
const POLL_MS = 2000;

interface Series {
  cpu: number[];
  mem: number[];
  rx: number[]; // rates, bytes/s
  tx: number[];
}

export default function Metrics() {
  const [now, setNow] = useState<MetricsResponse>();
  const [series, setSeries] = useState<Series>({ cpu: [], mem: [], rx: [], tx: [] });
  const [error, setError] = useState("");
  // The previous cumulative network counters and their time, to derive rates.
  const prev = useRef<{ rx: number; tx: number; t: number } | null>(null);

  usePoll(async (signal) => {
    try {
      const m = await system.metrics({}, { signal });
      setError("");
      setNow(m);
      const t = performance.now();
      const rx = Number(m.netRxBytes);
      const tx = Number(m.netTxBytes);
      let rxRate = 0;
      let txRate = 0;
      if (prev.current) {
        const dt = (t - prev.current.t) / 1000;
        if (dt > 0) {
          rxRate = Math.max(0, (rx - prev.current.rx) / dt);
          txRate = Math.max(0, (tx - prev.current.tx) / dt);
        }
      }
      prev.current = { rx, tx, t };
      setSeries((s) => ({
        cpu: push(s.cpu, m.cpuKnown ? m.cpuPercent : 0),
        mem: push(s.mem, m.memoryTotalBytes > 0n ? (Number(m.memoryUsedBytes) / Number(m.memoryTotalBytes)) * 100 : 0),
        rx: push(s.rx, rxRate),
        tx: push(s.tx, txRate),
      }));
    } catch (err) {
      if (!signal.aborted) setError(friendlyError(err));
    }
  }, POLL_MS);

  const memUsed = now ? Number(now.memoryUsedBytes) : 0;
  const memTotal = now ? Number(now.memoryTotalBytes) : 0;
  const rxNow = series.rx.at(-1) ?? 0;
  const txNow = series.tx.at(-1) ?? 0;

  return (
    <div className="metrics">
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}

      <section className="metrics__card">
        <div className="metrics__head">
          <h3 className="metrics__h">CPU</h3>
          <span className="metrics__now">{now?.cpuKnown ? percent(now.cpuPercent) : "measuring…"}</span>
        </div>
        <Sparkline values={series.cpu} max={100} className="spark--cpu" />
        <p className="metrics__sub">{now ? `${now.cpus.toFixed(now.cpus < 10 ? 1 : 0)} CPU${now.cpus === 1 ? "" : "s"} available` : "…"}</p>
      </section>

      <section className="metrics__card">
        <div className="metrics__head">
          <h3 className="metrics__h">Memory</h3>
          <span className="metrics__now">{memTotal > 0 ? `${bytes(memUsed)} / ${bytes(memTotal)}` : "…"}</span>
        </div>
        <Sparkline values={series.mem} max={100} className="spark--mem" />
        <p className="metrics__sub">{memTotal > 0 ? `${percent((memUsed / memTotal) * 100)} used` : ""}</p>
      </section>

      <section className="metrics__card">
        <div className="metrics__head">
          <h3 className="metrics__h">Network</h3>
          <span className="metrics__now">
            ↓ {rate(rxNow)} · ↑ {rate(txNow)}
          </span>
        </div>
        <Sparkline values={series.rx} className="spark--rx" />
        <p className="metrics__sub">{now ? `${bytes(Number(now.netRxBytes))} received · ${bytes(Number(now.netTxBytes))} sent since start` : ""}</p>
      </section>

      <section className="metrics__card metrics__card--wide">
        <div className="metrics__head">
          <h3 className="metrics__h">Disk</h3>
        </div>
        <div className="metrics__disks">
          {(now?.disks ?? []).map((d) => (
            <Disk key={d.path} disk={d} />
          ))}
          {now && now.disks.length === 0 && <p className="metrics__sub">No file systems reported.</p>}
        </div>
      </section>
    </div>
  );
}

function Disk({ disk }: { disk: DiskUsage }) {
  const total = Number(disk.totalBytes);
  const used = Number(disk.usedBytes);
  const frac = total > 0 ? used / total : 0;
  return (
    <div className="metrics__disk">
      <div className="metrics__diskhead">
        <code>{disk.path}</code>
        <span>
          {bytes(used)} / {bytes(total)}
        </span>
      </div>
      <div className="metrics__bar" role="meter" aria-label={`${disk.path} used`} aria-valuemin={0} aria-valuemax={total} aria-valuenow={used}>
        <div className={`metrics__barfill${frac > 0.9 ? " metrics__barfill--full" : ""}`} style={{ width: `${Math.min(frac * 100, 100)}%` }} />
      </div>
      <p className="metrics__sub">{bytes(Number(disk.availableBytes))} available</p>
    </div>
  );
}

function push(arr: number[], v: number): number[] {
  const next = [...arr, v];
  return next.length > HISTORY ? next.slice(next.length - HISTORY) : next;
}
