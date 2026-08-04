FROM node:24-bookworm-slim AS web-builder

WORKDIR /build/web

RUN corepack enable && corepack prepare pnpm@10.26.0 --activate

COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build

FROM rust:1.97-bookworm AS server-builder

WORKDIR /build

RUN apt-get update \
    && apt-get install --yes --no-install-recommends cmake libssl-dev pkg-config \
    && rm -rf /var/lib/apt/lists/*

COPY Cargo.toml Cargo.lock rust-toolchain.toml ./
COPY schema/ schema/
COPY src/ src/
RUN cargo build --release --locked

FROM debian:bookworm-slim AS runtime

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl libssl3 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home --home-dir /home/sumi sumi \
    && install --directory --owner=sumi --group=sumi /var/lib/sumi/attachments /opt/sumi/web

COPY --from=server-builder --chown=sumi:sumi /build/target/release/sumi /usr/local/bin/sumi
COPY --from=web-builder --chown=sumi:sumi /build/web/dist/ /opt/sumi/web/

ENV SUMI_SERVER__BIND=0.0.0.0:3000 \
    SUMI_SERVER__WEB_DIST=/opt/sumi/web \
    SUMI_SERVER__ATTACHMENT_DIR=/var/lib/sumi/attachments

USER sumi

EXPOSE 3000

ENTRYPOINT ["sumi"]
CMD ["server"]
