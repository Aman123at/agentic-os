// A small, dependency-free Markdown renderer for the Agent's prose (PLAN.md
// §M5.1). The model replies in Markdown; showing the raw ** and ` markers as
// plain text — and never wrapping the long lines a list makes — is noise. This
// covers the subset the Agent actually emits (headings, bold, italic, inline
// code, links, fenced code and unordered/ordered lists) and builds React nodes
// directly, never innerHTML, so nothing in a reply can inject markup. A full
// Markdown library would also blow the 150 KB bundle budget for one view.
import { Fragment, type ReactNode } from "react";

export function Markdown({ text, className }: { text: string; className?: string }) {
  return <div className={className ? `md ${className}` : "md"}>{blocks(text)}</div>;
}

const LIST = /^\s*([-*+]|\d+\.)\s+/;

// blocks splits the source into block-level elements: fenced code, headings,
// lists and paragraphs, separated by blank lines.
function blocks(src: string): ReactNode[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: ReactNode[] = [];
  let i = 0;
  let key = 0;
  while (i < lines.length) {
    const line = lines[i];

    if (/^```/.test(line)) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !/^```\s*$/.test(lines[i])) {
        body.push(lines[i]);
        i++;
      }
      i++; // skip the closing fence (absent at end of text is fine)
      out.push(
        <pre key={key++} className="md__code">
          <code>{body.join("\n")}</code>
        </pre>,
      );
      continue;
    }

    const h = line.match(/^(#{1,6})\s+(.*)$/);
    if (h) {
      const Tag = `h${Math.min(6, h[1].length + 2)}` as "h3";
      out.push(
        <Tag key={key++} className="md__h">
          {inline(h[2])}
        </Tag>,
      );
      i++;
      continue;
    }

    if (!line.trim()) {
      i++;
      continue;
    }

    if (LIST.test(line)) {
      const ordered = /^\s*\d+\.\s+/.test(line);
      const items: ReactNode[] = [];
      while (i < lines.length && LIST.test(lines[i])) {
        items.push(<li key={items.length}>{inline(lines[i].replace(LIST, ""))}</li>);
        i++;
      }
      out.push(
        ordered ? (
          <ol key={key++} className="md__list">
            {items}
          </ol>
        ) : (
          <ul key={key++} className="md__list">
            {items}
          </ul>
        ),
      );
      continue;
    }

    const para: string[] = [];
    while (i < lines.length && lines[i].trim() && !/^```/.test(lines[i]) && !/^#{1,6}\s/.test(lines[i]) && !LIST.test(lines[i])) {
      para.push(lines[i]);
      i++;
    }
    out.push(
      <p key={key++} className="md__p">
        {para.map((l, idx) => (
          <Fragment key={idx}>
            {idx > 0 && <br />}
            {inline(l)}
          </Fragment>
        ))}
      </p>,
    );
  }
  return out;
}

// inline turns one line into nodes, honouring `code`, **bold**, *italic* and
// [text](http…) links. Code is matched first so markers inside it stay literal;
// only http(s) links are linkified, so a reply can't smuggle a javascript: URL.
const INLINE =
  /`([^`]+)`|\*\*([^*]+?)\*\*|__([^_]+?)__|\*([^*]+?)\*|_([^_]+?)_|\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g;

function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let key = 0;
  let m: RegExpExecArray | null;
  INLINE.lastIndex = 0;
  while ((m = INLINE.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index));
    if (m[1] !== undefined) out.push(<code key={key++}>{m[1]}</code>);
    else if (m[2] !== undefined || m[3] !== undefined) out.push(<strong key={key++}>{m[2] ?? m[3]}</strong>);
    else if (m[4] !== undefined || m[5] !== undefined) out.push(<em key={key++}>{m[4] ?? m[5]}</em>);
    else
      out.push(
        <a key={key++} href={m[7]} target="_blank" rel="noreferrer noopener">
          {m[6]}
        </a>,
      );
    last = INLINE.lastIndex;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}
