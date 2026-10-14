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

# `mirrormark install` writes into these root-owned directories. The tests
# run as your UID without sudo, so in this image only, anyone may write
# them; the sticky bit stops users removing each other's files.
RUN mkdir -p /usr/share/google-chrome/extensions /etc/opt/chrome/native-messaging-hosts \
 && chmod 1777 /usr/share/google-chrome/extensions /etc/opt/chrome/native-messaging-hosts

# The source is bind-mounted at runtime; nothing is copied in.
WORKDIR /src
RUN git config --system --add safe.directory /src
