# Brave spike image: real Brave from Brave's own apt pool, Xvfb, python3.
# Build: docker build -t spike-brave docs/rfcs/0001_local-bookmark-sync-host.d/brave-spike
FROM ubuntu:24.04@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55
ENV DEBIAN_FRONTEND=noninteractive
# Version and checksum come from
# https://brave-browser-apt-release.s3.brave.com/dists/stable/main/binary-amd64/Packages
ARG BRAVE_VERSION=1.97.57
ARG BRAVE_SHA256=d79919a82aa869cf5121b641afa79958730c4bdaf36a0c51ae0f9c32f9c16bb9
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && curl -fsSLo /tmp/brave.deb "https://brave-browser-apt-release.s3.brave.com/pool/main/b/brave-browser/brave-browser_${BRAVE_VERSION}_amd64.deb" \
 && echo "${BRAVE_SHA256}  /tmp/brave.deb" | sha256sum -c - \
 && curl -fsSLo /usr/share/keyrings/brave-browser-archive-keyring.gpg https://brave-browser-apt-release.s3.brave.com/brave-browser-archive-keyring.gpg \
 && echo "deb [signed-by=/usr/share/keyrings/brave-browser-archive-keyring.gpg] https://brave-browser-apt-release.s3.brave.com/ stable main" > /etc/apt/sources.list.d/brave.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends /tmp/brave.deb xvfb xauth python3 jq procps \
 && rm -rf /var/lib/apt/lists/* /tmp/brave.deb
RUN useradd -m u
USER u
WORKDIR /home/u
