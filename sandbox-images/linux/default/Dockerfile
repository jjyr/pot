# Build context is prepared by scripts/build-container-image for the target CPU.
ARG ALPINE_IMAGE=docker.io/library/alpine@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8
FROM ${ALPINE_IMAGE}
COPY assets/packages.lock /opt/pot-packages.lock
RUN apk add --no-cache $(cat /opt/pot-packages.lock) && rm /opt/pot-packages.lock
COPY assets/ /opt/pot-assets/
RUN POT_IMAGE_PACKAGES_INSTALLED=1 POT_IMAGE_ASSETS=/opt/pot-assets /bin/sh /opt/pot-assets/install.sh \
    && rm -rf /opt/pot-assets /cache
RUN apk add --no-cache tini=0.19.0-r3
LABEL dev.pot.bootstrap="pot-executor-v2"
ENV LANG=C.UTF-8 LC_ALL=C.UTF-8
EXPOSE 17701
CMD ["/bin/sh", "/opt/pot/sandbox.sh"]
