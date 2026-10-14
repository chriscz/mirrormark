FROM ubuntu:24.04@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl xz-utils \
    libgtk-3-0t64 libasound2t64 libx11-xcb1 libdbus-glib-1-2 libxtst6 libpci3 xvfb xauth python3 zip unzip jq sqlite3 dbus dbus-x11 procps \
 && rm -rf /var/lib/apt/lists/*
ARG DEV_VERSION=159.0b2
ARG DEV_SHA256=710dfa80e9edcad6c31f5aa0b3dffb4164b14a60be09316621de3894d8944b40
RUN curl -fsSLo /tmp/ff.tar.xz https://download-installer.cdn.mozilla.net/pub/devedition/releases/${DEV_VERSION}/linux-x86_64/en-US/firefox-${DEV_VERSION}.tar.xz \
 && echo "${DEV_SHA256}  /tmp/ff.tar.xz" | sha256sum -c - && mkdir -p /opt/devedition && tar -C /opt/devedition --strip-components=1 -xJf /tmp/ff.tar.xz && rm /tmp/ff.tar.xz
ARG REL_VERSION=158.0
ARG REL_SHA256=9549b054ec4cf85f50f6a19d8c6fb79580312e71ebc4ec2cb217ec9af09dc12a
RUN curl -fsSLo /tmp/ff.tar.xz https://download-installer.cdn.mozilla.net/pub/firefox/releases/${REL_VERSION}/linux-x86_64/en-US/firefox-${REL_VERSION}.tar.xz \
 && echo "${REL_SHA256}  /tmp/ff.tar.xz" | sha256sum -c - && mkdir -p /opt/release && tar -C /opt/release --strip-components=1 -xJf /tmp/ff.tar.xz && rm /tmp/ff.tar.xz
COPY --from=mirrormark-dev:latest /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH
RUN useradd -m -s /bin/bash u
USER u
WORKDIR /home/u
