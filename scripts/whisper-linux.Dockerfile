FROM ubuntu:22.04

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    build-essential cmake ninja-build libvulkan-dev python3 curl ca-certificates xz-utils

# Jammy supplies the runtime loader; the pinned SDK supplies newer build-only headers and glslc.
# The pinned archive is 263291376 bytes. Bounded range requests avoid a slow single download.
RUN set -eu; \
    url=https://sdk.lunarg.com/sdk/download/1.3.296.0/linux/vulkan_sdk.tar.xz; \
    pids=""; \
    for part in 0 1 2 3 4 5 6 7; do \
        first=$((part * 32911422)); last=$((first + 32911421)); \
        curl -fsSL --retry 3 --connect-timeout 20 --max-time 300 --speed-time 30 --speed-limit 1024 \
            --max-filesize 32911422 --range "$first-$last" "$url" -o "/tmp/vulkan_sdk.$part" & \
        pids="$pids $!"; \
    done; \
    for pid in $pids; do wait "$pid"; done; \
    for part in 0 1 2 3 4 5 6 7; do test "$(stat -c %s "/tmp/vulkan_sdk.$part")" -eq 32911422; done; \
    cat /tmp/vulkan_sdk.0 /tmp/vulkan_sdk.1 /tmp/vulkan_sdk.2 /tmp/vulkan_sdk.3 \
        /tmp/vulkan_sdk.4 /tmp/vulkan_sdk.5 /tmp/vulkan_sdk.6 /tmp/vulkan_sdk.7 > /tmp/vulkan_sdk.tar.xz; \
    echo "79b0a1593dadc46180526250836f3e53688a9a5fb42a0e5859eb72316dc4d53e  /tmp/vulkan_sdk.tar.xz" | sha256sum -c -; \
    mkdir -p /opt/vulkan; \
    tar -xJf /tmp/vulkan_sdk.tar.xz -C /opt/vulkan; \
    rm /tmp/vulkan_sdk.*

ENV VULKAN_SDK=/opt/vulkan/1.3.296.0/x86_64
WORKDIR /src
