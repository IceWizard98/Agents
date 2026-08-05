# Thin layer over the official Hermes image: seed config + apply HERMES_MODEL from
# env on every boot (s6 cont-init), so the model is env-driven (Coolify: set the env
# var + redeploy — no manual step, no ${VAR} that Hermes won't expand).
FROM nousresearch/hermes-agent

# Install Go (cli-printing-press needs >= 1.26.5). PATH must be ENV so later
# RUN layers and `go install` output are on PATH — a `RUN export` dies with its layer.
# TARGETARCH (set by buildx) picks the matching tarball — an amd64 Go run under
# qemu emulation on an arm64 host corrupts its GC ("sweep increased allocation count").
ARG TARGETARCH
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH
RUN apt-get update && apt-get install -y wget && \
    wget https://go.dev/dl/go1.26.5.linux-${TARGETARCH}.tar.gz && \
    tar -C /usr/local -xzf go1.26.5.linux-${TARGETARCH}.tar.gz && \
    rm go1.26.5.linux-${TARGETARCH}.tar.gz && \
    go version && \
    # L'agent (terminal.backend=local) puo' girare comandi in login shell, che
    # resetta PATH da /etc/profile e scarta /usr/local/go/bin. /usr/local/bin e'
    # su ogni PATH: symlink qui cosi' `go` si trova in login e non-login shell.
    ln -sf /usr/local/go/bin/go /usr/local/bin/go && \
    ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt

# Pre-build the web dashboard dist so `hermes dashboard --skip-build` can serve it
# without npm at runtime (used by the hermes-dashboard service). ~10s, baked once.
RUN cd /opt/hermes/web && npm run build

COPY config.example.yaml /opt/hermes/template.config.yaml
COPY cont-init-hermes-config.sh /etc/cont-init.d/01-hermes-config
RUN chmod +x /etc/cont-init.d/01-hermes-config

# Install cli-printing-press for Hermes.
# Il binario va in /usr/local/bin (world-readable, su PATH) — i servizi runtime
# girano come utente non-root e non possono leggere /root/go/bin. L'installer
# ufficiale (curl | bash) metterebbe skill in ~/.claude/skills e binario in
# ~/go/bin: nessuna delle due posizioni è letta/raggiungibile da Hermes a runtime.
ENV GOBIN=/usr/local/bin
# GOGC basso = GC più aggressivo durante il link (binario ~77MB): taglia il picco
# di RAM su host piccoli/senza swap dove il linker va in "cannot allocate memory".
RUN GOGC=20 go install github.com/mvanhorn/cli-printing-press/v4/cmd/cli-printing-press@latest

# Le skill vanno nella dir bundled di Hermes (/opt/hermes/skills/<categoria>): al
# boot sync_skills le semina in ~/.hermes/skills di ogni profilo, l'unico posto
# che Hermes legge. I file del module cache sono read-only → chmod per l'utente
# runtime non-root.
RUN SKILLS_SRC="$(find /root/go/pkg/mod/github.com/mvanhorn -type d -path '*cli-printing-press/v4@*/skills' | head -1)" && \
    test -n "$SKILLS_SRC" && \
    mkdir -p /opt/hermes/skills/software-development && \
    cp -r "$SKILLS_SRC"/printing-press* /opt/hermes/skills/software-development/ && \
    chmod -R a+rX /opt/hermes/skills/software-development

# Chromium per far navigare il web all'agent (gira `chromium <url>` da shell, non
# via MCP). NON è leggero: chromium + libs Xlib/nss/font ~400-500MB; --no-install-
# recommends taglia solo gli extra (ffmpeg, font CJK). Ultimo layer: non invalida la
# cache di go install / npm build sopra.
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
