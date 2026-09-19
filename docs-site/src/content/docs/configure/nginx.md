---
title: Putting nginx in front
description: A reverse proxy and a certificate, with and without TLS.
---

STUB — page map only. This page exists so the sidebar and the shape of the site are
judgeable; M6 writes it.

Must carry: a working server block, `proxy_pass` to 127.0.0.1:7700, the WebSocket upgrade headers,
certbot, and — once TLS is in front — setting `bind: 127.0.0.1` so port 7700 stops answering the
internet directly.
