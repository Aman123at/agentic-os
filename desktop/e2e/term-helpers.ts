// Helpers for reading the terminal through its test-only hook (window.__aosTerm,
// set by apps/terminal/term.ts when the harness flag is on). xterm's buffer holds
// the text regardless of the WebGL renderer, so these work headless.
import type { Page } from "@playwright/test";

// termText returns the visible + scrollback text of the active terminal.
export function termText(page: Page): Promise<string> {
  return page.evaluate(() => {
    const term = (window as unknown as { __aosTerm?: AosTerm }).__aosTerm;
    if (!term) return "";
    const buf = term.buffer.active;
    let out = "";
    for (let i = 0; i < buf.length; i++) out += (buf.getLine(i)?.translateToString(true) ?? "") + "\n";
    return out;
  });
}

// Minimal shape of the xterm APIs these helpers touch.
interface AosTerm {
  buffer: { active: { length: number; getLine(i: number): { translateToString(trim?: boolean): string } | undefined } };
  onData(cb: (data: string) => void): { dispose(): void };
  onWriteParsed(cb: () => void): { dispose(): void };
}

// measureKeystrokeEcho types printable keys one at a time (the harness pairs each
// send with the echo the PTY writes back) and returns the per-keystroke latency
// in milliseconds. The first few samples are dropped as warm-up.
export async function measureKeystrokeEcho(page: Page, count: number): Promise<number[]> {
  await page.evaluate(() => {
    const term = (window as unknown as { __aosTerm?: AosTerm }).__aosTerm;
    const w = window as unknown as { __sends: number[]; __echos: number[]; __disp?: { dispose(): void }[] };
    w.__sends = [];
    w.__echos = [];
    w.__disp = [];
    if (!term) return;
    w.__disp.push(term.onData(() => w.__sends.push(performance.now())));
    w.__disp.push(term.onWriteParsed(() => w.__echos.push(performance.now())));
  });

  for (let i = 0; i < count; i++) {
    await page.keyboard.press("a");
    await page.waitForTimeout(90); // let the echo arrive and the line go idle
  }

  return page.evaluate(() => {
    const w = window as unknown as { __sends: number[]; __echos: number[]; __disp?: { dispose(): void }[] };
    for (const d of w.__disp ?? []) d.dispose();
    const out: number[] = [];
    for (const send of w.__sends) {
      const echo = w.__echos.find((e) => e >= send);
      if (echo !== undefined) out.push(echo - send);
    }
    return out.slice(3); // drop warm-up
  });
}
