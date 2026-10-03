FROM debian@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
# Snapshot fixes package resolution; no dependency on mutable latest apt indexes.
RUN rm -f /etc/apt/sources.list.d/debian.sources && printf '%s\n' \
 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian/20261001T000000Z bookworm main' \
 'deb [check-valid-until=no] http://snapshot.debian.org/archive/debian-security/20261001T000000Z bookworm-security main' > /etc/apt/sources.list \
 && apt-get -o Acquire::Check-Valid-Until=false update \
 && apt-get install -y --no-install-recommends chromium chromium-sandbox tigervnc-standalone-server fluxbox websockify python3 python3-websocket xdotool xclip x11-utils x11-xserver-utils fonts-noto-cjk ca-certificates curl procps tini \
 && dpkg-query -W > /opt-packages.txt && rm -rf /var/lib/apt/lists/*
ADD novnc.tar.gz /opt/
RUN mv /opt/noVNC-1.6.0 /opt/novnc && useradd -m -u 1000 pot \
 && mkdir /workspace /profile && chown pot:pot /workspace /profile
COPY assets /opt/pot/assets
COPY start.sh driver.py /opt/pot/
USER 1000:1000
ENV DISPLAY=:99
EXPOSE 6080
ENTRYPOINT ["/usr/bin/tini", "--", "/opt/pot/start.sh"]
