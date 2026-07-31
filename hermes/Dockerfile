# Thin layer over the official Hermes image: seed config + apply HERMES_MODEL from
# env on every boot (s6 cont-init), so the model is env-driven (Coolify: set the env
# var + redeploy — no manual step, no ${VAR} that Hermes won't expand).
FROM nousresearch/hermes-agent
# Pre-build the web dashboard dist so `hermes dashboard --skip-build` can serve it
# without npm at runtime (used by the hermes-dashboard service). ~10s, baked once.
RUN cd /opt/hermes/web && npm run build
COPY config.example.yaml /opt/hermes/template.config.yaml
COPY cont-init-hermes-config.sh /etc/cont-init.d/01-hermes-config
RUN chmod +x /etc/cont-init.d/01-hermes-config
