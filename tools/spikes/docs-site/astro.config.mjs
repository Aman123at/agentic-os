// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

// The site is uploaded to a sub-path of Aman's own domain, so `base` is not
// cosmetic: with the default "/", every asset URL and every internal link the
// build emits is absolute from the root and 404s once uploaded — and it fails
// only there, never in `astro dev` or `astro preview`.
//
// Nothing in this site may be published at /agentic-os/install.sh. That path is
// a redirect to raw.githubusercontent.com (issue 11, decision 8); a file here
// would shadow it and a docs deploy would start serving a web page to
// `curl | sudo sh`.
export default defineConfig({
  site: "https://amantiwari.co.in",
  base: "/agentic-os/",
  integrations: [
    starlight({
      title: "Agentic OS",
      description:
        "An Ubuntu machine your Agents run, on your own server. Install, configure, and every aos command.",
      social: [{ icon: "github", label: "GitHub", href: "https://github.com/Aman123at/agentic-os" }],
      // One version, deployed when a tag is cut, so the site can never describe
      // a command that is not in anyone's binary. Starlight has no built-in
      // versioning — it is the third-party starlight-versions plugin — and one
      // released version does not earn that dependency yet.
      sidebar: [
        { label: "Start here", items: ["index", "start/install", "start/compose", "start/password-is-root", "start/sign-in"] },
        { label: "Configure", items: ["configure/config-file", "configure/models", "configure/nginx"] },
        { label: "Use it", items: ["use/services", "use/browser", "use/restarts"] },
        { label: "Operate", items: ["operate/lost-password", "operate/upgrade", "operate/troubleshooting"] },
        { label: "Reference", items: [{ autogenerate: { directory: "reference" } }] },
      ],
    }),
  ],
});
