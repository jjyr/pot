FROM --platform=$BUILDPLATFORM node:24.21.0-bookworm-slim@sha256:d6aa754f16b3197301076f047b5def2f02ea1dbbc2ca920407d46d7ec7f87b20 AS node
FROM --platform=$BUILDPLATFORM python:3.12.13-slim-bookworm@sha256:4766d8b510c428e595d74b9cc5bbb2fae8e26316fffb4adc89908d79aacd58a2 AS python
FROM --platform=$BUILDPLATFORM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
COPY --from=node /usr/local/ /usr/local/
COPY --from=python /usr/local/ /usr/local/
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG POT_APP_VERSION=0.0.0
ARG POT_BUILD_REVISION=container-local
ARG POT_UPDATE_PUBLIC_KEY=
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    test "$TARGETOS" = linux && case "$TARGETARCH" in amd64|arm64) ;; *) exit 1 ;; esac \
    && export POT_APP_VERSION POT_BUILD_REVISION POT_UPDATE_PUBLIC_KEY \
    && python3 scripts/build-pot-cli --platform "linux-$TARGETARCH" --output /tmp/packages \
    && mkdir /out \
    && tar -xzf "/tmp/packages/pot-linux-$TARGETARCH.tar.gz" --strip-components=1 -C /out \
    && CGO_ENABLED=0 GOOS=linux GOARCH="$TARGETARCH" go build -mod=readonly -trimpath \
       -ldflags='-s -w' -o /out/bin/pot-sandbox-check ./docker/sandbox-check \
    && rm -rf /out/service /out/Resources/node/include \
    && chmod -R a+rX /out

FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8 AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
      bash bubblewrap ca-certificates git python3 ripgrep socat tini \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 pot && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash pot \
    && chmod 0700 /home/pot
COPY --from=build /out/ /opt/pot/
COPY docker/entrypoint.py docker/healthcheck.py /opt/pot/docker/
ENV HOME=/home/pot \
    PATH=/opt/pot/bin:/opt/pot/Resources/node/bin:/usr/local/bin:/usr/bin:/bin \
    POT_WEB_ORIGIN=http://localhost:17703 \
    PYTHONDONTWRITEBYTECODE=1
ARG POT_APP_VERSION=0.0.0
ARG POT_BUILD_REVISION=container-local
LABEL org.opencontainers.image.source="https://github.com/jjyr/pot" \
      org.opencontainers.image.title="Pot server" \
      org.opencontainers.image.version=$POT_APP_VERSION \
      org.opencontainers.image.revision=$POT_BUILD_REVISION
USER 1000:1000
WORKDIR /home/pot
VOLUME ["/home/pot"]
EXPOSE 17703/tcp
HEALTHCHECK --interval=30s --timeout=10s --start-period=40s --retries=3 \
    CMD ["python3", "/opt/pot/docker/healthcheck.py"]
ENTRYPOINT ["/usr/bin/tini", "--", "python3", "/opt/pot/docker/entrypoint.py"]
CMD ["server"]
