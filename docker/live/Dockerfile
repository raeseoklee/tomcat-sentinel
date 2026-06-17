FROM alpine:3.20

ARG TARGETARCH

COPY dist/tomcat-sentinel-linux-${TARGETARCH} /usr/local/bin/tomcat-sentinel
COPY docker/live/entrypoint.sh /usr/local/bin/tomcat-sentinel-live-smoke

RUN chmod +x /usr/local/bin/tomcat-sentinel /usr/local/bin/tomcat-sentinel-live-smoke

ENTRYPOINT ["/usr/local/bin/tomcat-sentinel-live-smoke"]

