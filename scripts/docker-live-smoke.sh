#!/bin/sh
set -eu

IMAGE="${IMAGE:-tomcat-sentinel:live-smoke}"

SERVER_ARCH="$(docker version --format '{{.Server.Arch}}' 2>/dev/null || uname -m)"
case "$SERVER_ARCH" in
  amd64|x86_64)
    TARGETARCH=amd64
    ;;
  arm64|aarch64)
    TARGETARCH=arm64
    ;;
  *)
    echo "unsupported docker server architecture: $SERVER_ARCH" >&2
    exit 1
    ;;
esac

make dist-linux VERSION=dev
docker build --build-arg TARGETARCH="$TARGETARCH" -f docker/live/Dockerfile -t "$IMAGE" .
docker run --rm "$IMAGE"
