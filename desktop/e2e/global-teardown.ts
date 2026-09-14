// Tear the test Machine down after the suite (PLAN.md §17, M3.5): compose down
// with its volumes, then remove the scratch folder. Set AOS_PW_KEEP=1 to leave
// the Machine running for a look afterwards.
import { rmSync } from "node:fs";

import { docker, loadCompose } from "./harness";

export default async function globalTeardown(): Promise<void> {
  let compose;
  try {
    compose = loadCompose();
  } catch {
    return; // setup never got far enough to record a Machine.
  }
  if (process.env.AOS_PW_KEEP) {
    console.log(`[pw] keeping the Machine (project ${compose.project}); scratch ${compose.dir}`);
    return;
  }
  try {
    docker(compose, ["down", "-v", "--remove-orphans"]);
  } catch (err) {
    console.warn("[pw] docker compose down failed:", err);
  }
  try {
    rmSync(compose.dir, { recursive: true, force: true });
  } catch {
    // best effort
  }
}
