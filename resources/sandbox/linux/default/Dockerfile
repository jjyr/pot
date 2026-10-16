# Build context and the immutable base are prepared for the selected target CPU.
ARG BASE_IMAGE
FROM ${BASE_IMAGE}
COPY assets/ /opt/pot-assets/
RUN POT_IMAGE_ASSETS=/opt/pot-assets /bin/sh /opt/pot-assets/install.sh \
    && rm -rf /opt/pot-assets /cache
LABEL dev.pot.bootstrap="pot-executor-v2"
ENV LANG=C.UTF-8 LC_ALL=C.UTF-8
EXPOSE 17701
CMD ["/bin/sh", "/opt/pot/sandbox.sh"]
