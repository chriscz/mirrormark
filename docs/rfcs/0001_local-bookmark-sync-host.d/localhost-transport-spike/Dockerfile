# Localhost-transport spike image: the dev image (Chrome 155, Brave 1.97.57,
# Firefox Developer Edition 159.0b2, LibreWolf 158.0-1, Xvfb, D-Bus, Go)
# plus zip for the XPI, xwd and ImageMagick for full-screen screenshots,
# iproute2 to give the container a public-looking address (run with
# --cap-add NET_ADMIN), and an unprivileged user u to run the browsers.
# Build: docker build -t spike-lt docs/rfcs/0001_local-bookmark-sync-host.d/localhost-transport-spike
FROM mirrormark-dev:latest
RUN apt-get update \
 && apt-get install -y --no-install-recommends zip x11-apps imagemagick jq procps iproute2 \
 && rm -rf /var/lib/apt/lists/*
RUN useradd -m -s /bin/bash u
