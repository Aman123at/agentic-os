// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

// The site is served at the root of its own subdomain
// (agenticos.amantiwari.co.in, a GitHub Pages custom domain — see public/CNAME),
// so `base` is "/": every asset URL and internal link the build emits is
// absolute from the root, which is exactly where the host serves them. A
// non-root `base` (e.g. "/agentic-os/") would prefix every asset with a path the
// subdomain does not have, and the whole site loads unstyled — and it fails only
// on the real host, never in `astro dev` or `astro preview`.
//
// Nothing in this site may be published at /install.sh. That path is a redirect
// to raw.githubusercontent.com (issue 11, decision 8); a file here would shadow
// it and a docs deploy would start serving a web page to `curl | sudo sh`.
export default defineConfig({
  site: "https://agenticos.amantiwari.co.in",
  base: "/",
  integrations: [
    starlight({
      title: "Agentic OS",
      description:
        "Turn any Linux server into a machine AI Agents operate for you. Install guides for every device, configuration, and every aos command.",
      logo: { src: "./src/assets/logo.svg", alt: "Agentic OS" },
      favicon: "/favicon.svg",
      customCss: ["./src/styles/custom.css"],
      head: [
        { tag: "link", attrs: { rel: "preconnect", href: "https://fonts.googleapis.com" } },
        { tag: "link", attrs: { rel: "preconnect", href: "https://fonts.gstatic.com", crossorigin: true } },
        {
          tag: "link",
          attrs: {
            rel: "stylesheet",
            href: "https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500;600&display=swap",
          },
        },
        { tag: "meta", attrs: { name: "theme-color", content: "#6366f1" } },
      ],
      social: [{ icon: "github", label: "GitHub", href: "https://github.com/Aman123at/agentic-os" }],
      editLink: { baseUrl: "https://github.com/Aman123at/agentic-os/edit/main/docs-site/" },
      expressiveCode: {
        themes: ["github-dark-default", "github-light-default"],
        styleOverrides: {
          borderRadius: "0.75rem",
          codeFontFamily: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace",
          codeFontSize: "0.85rem",
          frames: { shadowColor: "transparent" },
        },
      },
      // One version, deployed when a tag is cut, so the site can never describe
      // a command that is not in anyone's binary. Starlight has no built-in
      // versioning — it is the third-party starlight-versions plugin — and one
      // released version does not earn that dependency yet.
      sidebar: [
        {
          label: "Get started",
          items: ["start/introduction", "start/quickstart", "start/choose", "start/sign-in"],
        },
        {
          label: "Install",
          items: [
            { label: "Linux server (recommended)", slug: "start/install" },
            { label: "Cloud providers", slug: "start/cloud" },
            { label: "Raspberry Pi & ARM", slug: "start/raspberry-pi" },
            { label: "Docker on macOS", slug: "start/macos" },
            { label: "Docker on Windows", slug: "start/windows" },
            { label: "Docker Compose (any OS)", slug: "start/compose" },
          ],
        },
        {
          label: "Configure",
          items: ["configure/api-key", "configure/config-file", "configure/models", "configure/nginx"],
        },
        { label: "Use it", items: ["use/services", "use/browser", "use/restarts", "use/root-mode"] },
        {
          label: "Operate",
          items: ["start/password-is-root", "operate/lost-password", "operate/upgrade", "operate/troubleshooting"],
        },
        { label: "Command reference", collapsed: true, items: [{ autogenerate: { directory: "reference" } }] },
      ],
    }),
  ],
});
