# syntax=docker/dockerfile:1

FROM debian:trixie-slim AS nginx-ui
ARG TARGETARCH
ARG NGINX_UI_VERSION=2.9.1
ARG NGINX_UI_SHA256_AMD64=8caa38db01a1b97b742c07015d0e19269c2cd13fdcd08aa9e948a5435d35fb08
ARG NGINX_UI_SHA256_ARM64=efe481a5842c0f6583fb1ef7229b6d9eb0a5b03d17fdcd3612287ff0c1e1061f
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends binutils ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN arch="${TARGETARCH:-$(dpkg --print-architecture)}"; \
    case "$arch" in \
        amd64) NGXUI_ARCH="linux-64"; NGXUI_SHA256="${NGINX_UI_SHA256_AMD64}" ;; \
        arm64) NGXUI_ARCH="linux-arm64-v8a"; NGXUI_SHA256="${NGINX_UI_SHA256_ARM64}" ;; \
        *) echo "unsupported architecture: $arch" >&2; exit 1 ;; \
    esac \
    && curl -fsSL -o /tmp/nginx-ui.tar.gz \
        "https://github.com/0xJacky/nginx-ui/releases/download/v${NGINX_UI_VERSION}/nginx-ui-${NGXUI_ARCH}.tar.gz" \
    && printf '%s  %s\n' "${NGXUI_SHA256}" /tmp/nginx-ui.tar.gz > /tmp/nginx-ui.sha256 \
    && sha256sum -c /tmp/nginx-ui.sha256 \
    && mkdir -p /out \
    && tar -xzf /tmp/nginx-ui.tar.gz -C /out \
    && test -x /out/nginx-ui \
    && strip --strip-unneeded /out/nginx-ui \
    && /out/nginx-ui --version \
    && apt-get purge -y --auto-remove binutils \
    && rm -rf /var/lib/apt/lists/*

FROM debian:trixie-slim AS s6
ARG TARGETARCH
ARG S6_OVERLAY_VERSION=3.2.3.2
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl xz-utils \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /tmp
RUN arch="${TARGETARCH:-$(dpkg --print-architecture)}"; \
    case "$arch" in \
        amd64) S6_ARCH="x86_64" ;; \
        arm64) S6_ARCH="aarch64" ;; \
        *) echo "unsupported architecture: $arch" >&2; exit 1 ;; \
    esac \
    && curl -fsSLO "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz" \
    && curl -fsSLO "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz.sha256" \
    && curl -fsSLO "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-${S6_ARCH}.tar.xz" \
    && curl -fsSLO "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-${S6_ARCH}.tar.xz.sha256" \
    && sha256sum -c s6-overlay-noarch.tar.xz.sha256 \
    && sha256sum -c "s6-overlay-${S6_ARCH}.tar.xz.sha256" \
    && mkdir -p /out \
    && tar -C /out -Jxpf s6-overlay-noarch.tar.xz \
    && tar -C /out -Jxpf "s6-overlay-${S6_ARCH}.tar.xz"

FROM scratch AS runtime-files
COPY --from=s6 /out/ /
COPY --from=nginx-ui /out/nginx-ui /usr/local/bin/nginx-ui
COPY rootfs/ /

FROM debian:trixie-slim
ARG DEBIAN_FRONTEND=noninteractive
# Nginx UI invokes logrotate directly. Selecting anacron satisfies logrotate's
# scheduler dependency without pulling cron and its systemd dependency chain.
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        logrotate \
        anacron \
        nginx \
        libnginx-mod-http-brotli-filter \
        libnginx-mod-http-brotli-static \
        libnginx-mod-http-cache-purge \
        libnginx-mod-http-dav-ext \
        libnginx-mod-http-echo \
        libnginx-mod-http-geoip2 \
        libnginx-mod-http-headers-more-filter \
        libnginx-mod-http-image-filter \
        libnginx-mod-http-lua \
        libnginx-mod-http-ndk \
        libnginx-mod-http-subs-filter \
        libnginx-mod-http-upstream-fair \
        libnginx-mod-http-xslt-filter \
        libnginx-mod-rtmp \
        libnginx-mod-stream \
        libnginx-mod-stream-geoip2 \
    && mkdir -p /etc/nginx-ui /var/log/nginx \
    && touch /var/log/nginx/access.log /var/log/nginx/error.log \
    && rm -rf /var/lib/apt/lists/*

COPY --from=runtime-files / /

EXPOSE 80 443 9001
ENTRYPOINT ["/init"]
