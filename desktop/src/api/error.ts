// One place that turns an RPC failure into a sentence a person can read
// (PLAN.md M4.8 item 8.13). Connect prefixes its message with the code, so a
// failed lock used to reach the user as
// "[not_found] lstat /home/aos/nope: no such file or directory"; the layers
// under aosd sometimes hand back a raw syscall error the same way.
import { ConnectError } from "@connectrpc/connect";

// The Go syscall wrappers all format as "<op> <path>: <reason>".
const SYSCALL = /^(?:l?stat|open|openat|read|readdir|write|mkdir|remove|rename|unlink|chmod|chown)\s+(\S+):\s*(.+)$/;

const REASONS: Record<string, (path: string) => string> = {
  "no such file or directory": (p) => `There is nothing at ${p}.`,
  "permission denied": (p) => `AOS is not allowed to touch ${p}.`,
  "file exists": (p) => `Something is already at ${p}.`,
  "directory not empty": (p) => `${p} is not empty.`,
  "not a directory": (p) => `${p} is not a folder.`,
  "is a directory": (p) => `${p} is a folder.`,
};

export function friendlyError(err: unknown): string {
  const message = ConnectError.from(err).message.replace(/^\[[a-z_]+\]\s*/, "");
  const m = SYSCALL.exec(message);
  if (!m) return sentence(message);
  const say = REASONS[m[2]];
  return say ? say(m[1]) : sentence(`${m[1]}: ${m[2]}`);
}

// Go error strings start lower-case by convention; a sentence shown to a person
// should not.
function sentence(s: string): string {
  return /^[a-z]/.test(s) ? s[0].toUpperCase() + s.slice(1) : s;
}
