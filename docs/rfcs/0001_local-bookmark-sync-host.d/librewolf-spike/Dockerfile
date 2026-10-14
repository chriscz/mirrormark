# LibreWolf 158.0-1, the official Linux tarball, beside the Firefox spike's
# image (Xvfb, D-Bus, Go, Developer Edition for comparison).
FROM spike-firefox:latest
USER root
ARG LW_VERSION=158.0-1
ARG LW_SHA256=34bb7296feec0c6823abdfaf4fd50e47e9eada9cb4a5ed16e111a951dc9eb16c
RUN curl -fsSLo /tmp/lw.tar.xz https://dl.librewolf.net/librewolf/${LW_VERSION}/librewolf-${LW_VERSION}-linux-x86_64-package.tar.xz \
 && echo "${LW_SHA256}  /tmp/lw.tar.xz" | sha256sum -c - && mkdir -p /opt/librewolf && tar -C /opt/librewolf --strip-components=1 -xJf /tmp/lw.tar.xz && rm /tmp/lw.tar.xz
USER u
WORKDIR /home/u
