// The editor's look, shared by TextEdit and Code (PLAN.md M8.3): CodeMirror
// themed from the Desktop's CSS variables, so it follows light and dark.
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { EditorView } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";

export const theme = EditorView.theme({
  "&": { height: "100%", backgroundColor: "var(--window-bg)", color: "var(--text)", fontSize: "13px" },
  ".cm-scroller": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", lineHeight: "1.5" },
  ".cm-gutters": { backgroundColor: "var(--panel)", color: "var(--muted)", borderRight: "1px solid var(--border)" },
  ".cm-activeLineGutter, .cm-activeLine": { backgroundColor: "rgba(127, 127, 127, 0.1)" },
  ".cm-cursor": { borderLeftColor: "var(--text)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": { backgroundColor: "rgba(80, 140, 255, 0.3)" },
});

// Code colours come from the theme's --code-* variables, so they read well in
// both the light and dark themes.
export const highlight = syntaxHighlighting(
  HighlightStyle.define([
    { tag: [t.keyword, t.controlKeyword, t.operatorKeyword, t.modifier, t.heading], color: "var(--code-keyword)" },
    { tag: [t.string, t.special(t.string), t.regexp, t.link], color: "var(--code-string)" },
    { tag: [t.number, t.bool, t.null, t.atom, t.propertyName, t.constant(t.variableName)], color: "var(--code-number)" },
    { tag: [t.comment, t.meta, t.processingInstruction], color: "var(--code-comment)", fontStyle: "italic" },
    { tag: [t.function(t.variableName), t.function(t.propertyName), t.definition(t.function(t.variableName))], color: "var(--code-function)" },
    { tag: [t.typeName, t.className, t.namespace], color: "var(--code-type)" },
    { tag: [t.tagName, t.attributeName], color: "var(--code-tag)" },
    { tag: t.heading, fontWeight: "bold" },
    { tag: t.emphasis, fontStyle: "italic" },
    { tag: t.strong, fontWeight: "bold" },
    { tag: t.invalid, color: "var(--danger)" },
  ]),
);
