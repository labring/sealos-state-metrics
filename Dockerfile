FROM gcr.io/distroless/static:nonroot
ARG TARGETARCH
COPY bin/service-state-metrics-$TARGETARCH /state-metrics
EXPOSE 2222
USER 65532:65532

ENTRYPOINT [ "/state-metrics" ]