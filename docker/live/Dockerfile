FROM alpine:3.20

ARG TARGETARCH

COPY dist/jvm-sentinel-linux-${TARGETARCH} /usr/local/bin/jvm-sentinel
COPY docker/live/entrypoint.sh /usr/local/bin/jvm-sentinel-live-smoke

RUN chmod +x /usr/local/bin/jvm-sentinel /usr/local/bin/jvm-sentinel-live-smoke

ENTRYPOINT ["/usr/local/bin/jvm-sentinel-live-smoke"]

