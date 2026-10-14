# Dev image. Everything builds and tests in here: bin/dev run make
FROM ubuntu:24.04@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git make \
 && rm -rf /var/lib/apt/lists/*

ARG GO_VERSION=1.27.2
ARG GO_SHA256=ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5
RUN curl -fsSLo /tmp/go.tgz "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" \
 && echo "${GO_SHA256}  /tmp/go.tgz" | sha256sum -c - \
 && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
ENV PATH=/usr/local/go/bin:$PATH

# The source is bind-mounted at runtime; nothing is copied in.
WORKDIR /src
RUN git config --system --add safe.directory /src
