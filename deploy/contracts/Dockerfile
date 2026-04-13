FROM ghcr.io/foundry-rs/foundry:latest
USER root
# foundry:latest is Ubuntu 22.04-based
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash jq curl \
 && rm -rf /var/lib/apt/lists/*
COPY deploy.sh /deploy.sh
RUN chmod +x /deploy.sh
ENTRYPOINT ["/deploy.sh"]
