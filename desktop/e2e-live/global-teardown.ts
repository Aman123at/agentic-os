// Tear the live Machine down after the suite (PLAN.md §17, M4.7) and, above all,
// report what the run cost (PLAN.md §19: actual spend is reported after each live
// run). Set AOS_LIVE_KEEP=1 to leave the Machine running for a look afterwards.
import { rmSync } from "node:fs";

import { docker, loadLiveCompose, readSpend } from "./live-harness";

export default async function globalTeardown(): Promise<void> {
  reportSpend();

  let compose;
  try {
    compose = loadLiveCompose();
  } catch {
    return; // setup never got far enough to record a Machine.
  }

  if (process.env.AOS_LIVE_KEEP) {
    console.log(`[live] keeping the Machine (project ${compose.project}); scratch ${compose.dir}`);
    return;
  }
  try {
    docker(compose, ["down", "-v", "--remove-orphans"]);
  } catch (err) {
    console.warn("[live] docker compose down failed:", err);
  }
  try {
    rmSync(compose.dir, { recursive: true, force: true });
  } catch {
    // best effort
  }
}

function reportSpend(): void {
  const ledger = readSpend();
  console.log("\n[live] ==== spend for this run ====");
  if (ledger.length === 0) {
    console.log("[live] no Tasks recorded a cost.");
    return;
  }
  let total = 0;
  let anyUnpriced = false;
  for (const e of ledger) {
    if (e.costKnown && e.costUsd != null) {
      total += e.costUsd;
      console.log(`[live]   $${e.costUsd.toFixed(4)}  ${e.task}`);
    } else {
      anyUnpriced = true;
      console.log(`[live]   (cost unknown — model not in prices.yaml)  ${e.task}`);
    }
  }
  console.log(`[live] total: $${total.toFixed(4)}${anyUnpriced ? " (plus unpriced Tasks above)" : ""}`);
  console.log("[live] ================================\n");
}
