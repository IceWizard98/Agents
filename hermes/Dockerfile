# Thin layer over the official Hermes image: seed config + apply HERMES_MODEL from
# env on every boot (s6 cont-init), so the model is env-driven (Coolify: set the env
# var + redeploy — no manual step, no ${VAR} that Hermes won't expand).
# Pinned by digest: this image holds every stack credential — a floating tag would
# silently pull whatever upstream pushed on the next rebuild. Bump deliberately.
FROM nousresearch/hermes-agent@sha256:94f9348a221e63a83c239e5b292a682df246c99ae63bc4ed2c6ffe33d7bde0e3

# curl: usato dall'agent a runtime (fetch HTTP da shell).
RUN apt-get update && apt-get install -y --no-install-recommends curl && \
    rm -rf /var/lib/apt/lists/*

# Pre-build the web dashboard dist so `hermes dashboard --skip-build` can serve it
# without npm at runtime (used by the hermes-dashboard service). ~10s, baked once.
RUN cd /opt/hermes/web && npm run build

COPY config.example.yaml /opt/hermes/template.config.yaml
COPY cont-init-hermes-config.sh /etc/cont-init.d/01-hermes-config
RUN chmod +x /etc/cont-init.d/01-hermes-config

# Chromium per far navigare il web all'agent (gira `chromium <url>` da shell, non
# via MCP). NON è leggero: chromium + libs Xlib/nss/font ~400-500MB; --no-install-
# recommends taglia solo gli extra (ffmpeg, font CJK). Ultimo layer: non invalida la
# cache di npm build sopra.
# In un container chromium NON parte senza --no-sandbox (da root abortisce; da utente
# non-root manca lo userns) e crasha se /dev/shm è piccolo (--disable-dev-shm-usage).
# Il wrapper in /usr/local/bin (prima di /usr/bin su PATH) forza i flag per QUALSIASI
# chiamante — sia `chromium` a mano sia i tool che seguono CHROME_BIN.
ENV CHROME_BIN=/usr/local/bin/chromium
RUN apt-get update && apt-get install -y --no-install-recommends \
        chromium fonts-liberation && \
    rm -rf /var/lib/apt/lists/* && \
    printf '#!/bin/sh\nexec /usr/bin/chromium --no-sandbox --disable-dev-shm-usage "$@"\n' \
        > /usr/local/bin/chromium && \
    chmod a+rx /usr/local/bin/chromium
