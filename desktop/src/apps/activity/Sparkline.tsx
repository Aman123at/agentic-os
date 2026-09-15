// A small sparkline for Activity Monitor's CPU/Memory/Disk/Network graphs
// (PLAN.md §4.3, M4.4): a filled area under a line over the recent samples,
// drawn as an SVG that scales to its box. The newest sample is at the right.
interface SparklineProps {
  values: number[];
  // The top of the scale; defaults to the largest sample. A fixed max (e.g. 100
  // for a percentage) keeps the line from rescaling on every tick.
  max?: number;
  className?: string;
}

const W = 100;
const H = 32;

export function Sparkline({ values, max, className }: SparklineProps) {
  const top = Math.max(max ?? 0, ...values, 1e-9);
  const n = values.length;
  const points = values.map((v, i) => {
    const x = n <= 1 ? W : (i / (n - 1)) * W;
    const y = H - (Math.max(0, v) / top) * H;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const line = points.join(" ");
  const area = n > 0 ? `0,${H} ${line} ${W},${H}` : "";

  return (
    <svg className={`spark ${className ?? ""}`} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true">
      {n > 1 && <polygon className="spark__area" points={area} />}
      {n > 1 && <polyline className="spark__line" points={line} />}
    </svg>
  );
}
