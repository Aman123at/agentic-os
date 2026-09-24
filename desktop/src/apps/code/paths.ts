// Path helpers for Code, over the Machine's absolute POSIX paths.

// dirOf is the folder holding path ("/" for a top-level entry).
export function dirOf(path: string): string {
  const i = path.lastIndexOf("/");
  return i <= 0 ? "/" : path.slice(0, i);
}

export function baseOf(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1) || path;
}

export function join(dir: string, name: string): string {
  return dir.endsWith("/") ? dir + name : `${dir}/${name}`;
}

// within reports whether path is dir or inside it.
export function within(path: string, dir: string): boolean {
  return path === dir || path.startsWith(dir.endsWith("/") ? dir : `${dir}/`);
}

// relative is path from root, for a tab's tooltip and the status bar.
export function relative(path: string, root: string): string {
  if (path === root) return baseOf(path);
  return within(path, root) ? path.slice(root.length + (root.endsWith("/") ? 0 : 1)) : path;
}
