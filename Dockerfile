# Dev image. Everything builds and tests in here: bin/dev run make
FROM ubuntu:24.04@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55

ENV DEBIAN_FRONTEND=noninteractive
# gcc and libc6-dev only for go test -race, which needs cgo; the binary
# itself builds with CGO_ENABLED=0.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git make \
      gcc=4:13.2.0-7ubuntu1 libc6-dev=2.39-0ubuntu8.9 \
 && rm -rf /var/lib/apt/lists/*

ARG GO_VERSION=1.27.2
ARG GO_SHA256=ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5
RUN curl -fsSLo /tmp/go.tgz "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" \
 && echo "${GO_SHA256}  /tmp/go.tgz" | sha256sum -c - \
 && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
ENV PATH=/usr/local/go/bin:$PATH

# Google Chrome stable and Xvfb for the end-to-end tests (make check-e2e).
# Google removes old packages from its pool; when this download fails, bump
# the version and checksum together.
ARG CHROME_VERSION=155.0.8059.79-1
ARG CHROME_SHA256=b7726070ca707965e9a17b38e4f15ba283bf6f65c339626c092862af7b19badc
RUN curl -fsSLo /tmp/chrome.deb "https://dl.google.com/linux/chrome/deb/pool/main/g/google-chrome-stable/google-chrome-stable_${CHROME_VERSION}_amd64.deb" \
 && echo "${CHROME_SHA256}  /tmp/chrome.deb" | sha256sum -c - \
 && apt-get update \
 && apt-get install -y --no-install-recommends /tmp/chrome.deb xvfb xauth \
 && rm -rf /var/lib/apt/lists/* /tmp/chrome.deb

# Firefox Developer Edition for the Firefox end-to-end tests: release
# Firefox refuses unsigned add-ons. D-Bus carries a second firefox
# invocation's URL to the running one, which binding a running profile
# needs. Mozilla keeps old releases on its CDN; bump version and checksum
# together.
ARG FIREFOX_DEV_VERSION=159.0b2
ARG FIREFOX_DEV_SHA256=710dfa80e9edcad6c31f5aa0b3dffb4164b14a60be09316621de3894d8944b40
RUN apt-get update \
 && apt-get install -y --no-install-recommends xz-utils unzip libgtk-3-0t64 libasound2t64 \
      libx11-xcb1 libdbus-glib-1-2 libxtst6 libpci3 dbus dbus-x11 \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSLo /tmp/ff.tar.xz "https://download-installer.cdn.mozilla.net/pub/devedition/releases/${FIREFOX_DEV_VERSION}/linux-x86_64/en-US/firefox-${FIREFOX_DEV_VERSION}.tar.xz" \
 && echo "${FIREFOX_DEV_SHA256}  /tmp/ff.tar.xz" | sha256sum -c - \
 && mkdir -p /opt/firefox-devedition \
 && tar -C /opt/firefox-devedition --strip-components=1 -xJf /tmp/ff.tar.xz \
 && rm /tmp/ff.tar.xz

# Brave stable for the Brave end-to-end tests, from Brave's own apt pool.
# Brave removes old packages from the pool; bump version and checksum
# together (both are in the pool's dists/stable/main/binary-amd64/Packages).
# The package depends on brave-keyring, which only the pool's repository has.
ARG BRAVE_VERSION=1.97.57
ARG BRAVE_SHA256=d79919a82aa869cf5121b641afa79958730c4bdaf36a0c51ae0f9c32f9c16bb9
RUN curl -fsSLo /tmp/brave.deb "https://brave-browser-apt-release.s3.brave.com/pool/main/b/brave-browser/brave-browser_${BRAVE_VERSION}_amd64.deb" \
 && echo "${BRAVE_SHA256}  /tmp/brave.deb" | sha256sum -c - \
 && curl -fsSLo /usr/share/keyrings/brave-browser-archive-keyring.gpg https://brave-browser-apt-release.s3.brave.com/brave-browser-archive-keyring.gpg \
 && echo "deb [signed-by=/usr/share/keyrings/brave-browser-archive-keyring.gpg] https://brave-browser-apt-release.s3.brave.com/ stable main" > /etc/apt/sources.list.d/brave.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends /tmp/brave.deb \
 && rm -rf /var/lib/apt/lists/* /tmp/brave.deb

# LibreWolf for the LibreWolf end-to-end tests: the official tarball, using
# the libraries installed for Firefox above. Bump version and checksum (the
# release's .sha256sum file) together.
ARG LIBREWOLF_VERSION=158.0-1
ARG LIBREWOLF_SHA256=34bb7296feec0c6823abdfaf4fd50e47e9eada9cb4a5ed16e111a951dc9eb16c
RUN curl -fsSLo /tmp/lw.tar.xz "https://dl.librewolf.net/librewolf/${LIBREWOLF_VERSION}/librewolf-${LIBREWOLF_VERSION}-linux-x86_64-package.tar.xz" \
 && echo "${LIBREWOLF_SHA256}  /tmp/lw.tar.xz" | sha256sum -c - \
 && mkdir -p /opt/librewolf \
 && tar -C /opt/librewolf --strip-components=1 -xJf /tmp/lw.tar.xz \
 && rm /tmp/lw.tar.xz

# `mirrormark install` writes into these root-owned directories. The tests
# run as your UID without sudo, so in this image only, anyone may write
# them; the sticky bit stops users removing each other's files.
RUN mkdir -p /usr/share/google-chrome/extensions /opt/brave.com/brave/extensions /etc/opt/chrome/native-messaging-hosts \
 && chmod 1777 /usr/share/google-chrome/extensions /opt/brave.com/brave/extensions /etc/opt/chrome/native-messaging-hosts

# The source is bind-mounted at runtime; nothing is copied in.
WORKDIR /src
RUN git config --system --add safe.directory /src
