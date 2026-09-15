// Software's Packages view (PLAN.md §11): the software the Install Ledger says
// the Machine has, from SoftwareService.ListPackages, grouped by manager (apt,
// pipx, npm…). Packages installed as a dependency of another are marked. A filter
// narrows a long list.
import { ConnectError } from "@connectrpc/connect";
import { useCallback, useEffect, useMemo, useState } from "react";

import { software } from "../../api/client";
import type { Package } from "../../gen/aos/v1/types_pb";

export default function Packages() {
  const [packages, setPackages] = useState<Package[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await software.listPackages({});
      setError("");
      setPackages(resp.packages);
    } catch (err) {
      setError(ConnectError.from(err).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const groups = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const matched = q ? packages.filter((p) => p.name.toLowerCase().includes(q)) : packages;
    const byManager = new Map<string, Package[]>();
    for (const p of matched) {
      const list = byManager.get(p.manager) ?? [];
      list.push(p);
      byManager.set(p.manager, list);
    }
    for (const list of byManager.values()) list.sort((a, b) => a.name.localeCompare(b.name));
    return [...byManager.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  }, [packages, filter]);

  return (
    <div className="pkgs">
      <div className="sw__toolbar">
        <input className="activity__search" type="search" placeholder="Filter packages" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Filter packages" />
        <span className="activity__count">{loading ? "Loading…" : `${packages.length} package${packages.length === 1 ? "" : "s"}`}</span>
      </div>
      {error && (
        <div className="tasks__error" role="alert">
          {error}
        </div>
      )}
      {!loading && packages.length === 0 && !error ? (
        <div className="agent__empty">No software has been installed through AOS yet.</div>
      ) : (
        <div className="pkgs__list">
          {groups.map(([manager, list]) => (
            <section key={manager} className="pkgs__group">
              <h3 className="pkgs__manager">
                {manager} <span className="pkgs__mcount">{list.length}</span>
              </h3>
              <ul>
                {list.map((p) => (
                  <li key={`${p.manager}:${p.name}`} className="pkgs__pkg">
                    <span className="pkgs__name">{p.name}</span>
                    <span className="pkgs__ver">{p.version}</span>
                    {p.auto && (
                      <span className="pkgs__auto" title="Installed as a dependency of another package">
                        dependency
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}
