// Tear the test Machine down after the suite (PLAN.md §17, M3.5): check aosd's
// idle memory once more (§16; the first check is at startup), then compose down
// with its volumes and remove the scratch folder. Set AOS_PW_KEEP=1 to leave the
// Machine running for a look afterwards.
import { rmSync } from "node:fs";

import { IDLE_MS, checkIdleMemory, docker, loadCompose } from "./harness";

export default async function globalTeardown(): Promise<void> {
  let compose;
  try {
    compose = loadCompose();
  } catch {
    return; // setup never got far enough to record a Machine.
  }

  // Measure before tearing down, and fail only after cleaning up.
  let failure: unknown;
  try {
    await new Promise((r) => setTimeout(r, IDLE_MS));
    checkIdleMemory(compose, "after the suite");
  } catch (err) {
    failure = err;
  }

  if (process.env.AOS_PW_KEEP) {
    console.log(`[pw] keeping the Machine (project ${compose.project}); scratch ${compose.dir}`);
  } else {
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
  if (failure) throw failure;
}
