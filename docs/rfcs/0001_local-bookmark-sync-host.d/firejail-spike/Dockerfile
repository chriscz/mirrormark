# Firejail spike image: the dev image plus Ubuntu 24.04's firejail 0.9.72,
# its profiles and xdg-dbus-proxy (which firejail's Firefox profiles use
# to filter D-Bus). Build:
#   docker build -t spike-firejail docs/rfcs/0001_local-bookmark-sync-host.d/firejail-spike
FROM mirrormark-dev
RUN apt-get update \
 && apt-get install -y --no-install-recommends firejail firejail-profiles xdg-dbus-proxy jq procps \
 && rm -rf /var/lib/apt/lists/*
RUN useradd -m u
