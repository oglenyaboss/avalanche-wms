# Пинимся на stable — плавающий latest = nightly, регрессии flag-semantics
# (`forge create --broadcast`, `.deployedTo` в JSON) ломают деплой без предупреждения.
FROM ghcr.io/foundry-rs/foundry:stable
USER root
# foundry:stable is Ubuntu 22.04-based
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash jq curl \
 && rm -rf /var/lib/apt/lists/*
COPY deploy.sh /deploy.sh
RUN chmod +x /deploy.sh
ENTRYPOINT ["/deploy.sh"]
