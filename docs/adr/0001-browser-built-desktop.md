# The Desktop is a web app, not a streamed Linux desktop

The Desktop is a macOS-style interface built as a web application whose apps (Finder, Terminal, Agent, TextEdit, …) talk directly to `aosd`, rather than a real X11/Wayland desktop streamed to the browser via VNC/WebRTC. We chose this because "fast and smooth" is the primary goal: native DOM rendering gives 60 fps interaction, crisp text, negligible bandwidth, a small image, and instant reflection of Agent activity, whereas pixel streaming adds latency, blur, 1.5–3 GB of image weight, and forces Agents into slow screenshot-driven control.

## Consequences

- The Desktop cannot run arbitrary Linux GUI programs (GIMP, Firefox, …). Only its built-in apps and command-line software are available.
- Agents act through structured Tools, never by driving a screen.
- A future "streamed app window" (a single X11 app inside a Desktop window) remains possible as an addition, not a replacement.

## Considered Options

- **Streamed real desktop** (XFCE + macOS theme + KasmVNC/noVNC/Selkies): rejected for latency, weight, and superficial macOS fidelity.
- **Hybrid from day one**: deferred; it doubles the v1 surface for a capability the core use cases don't need.
