# syntax=docker/dockerfile:1.7
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
COPY data ./data
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" -o /out/eraser ./cmd/eraser \
    && mkdir -p /out/state && chmod 0700 /out/state \
    && printf 'eraser:x:65532:65532:Eraser:/data:/sbin/nologin\n' > /out/passwd \
    && printf 'eraser:x:65532:\n' > /out/group

FROM scratch
LABEL org.opencontainers.image.title="Eraser hardened" \
      org.opencontainers.image.source="https://git.nicholstech.org/Nichols-HomeLab/eraser"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/passwd /etc/passwd
COPY --from=build /out/group /etc/group
COPY --from=build /out/eraser /eraser
COPY --from=build /src/data/brokers.yaml /app/data/brokers.yaml
COPY --from=build --chown=65532:65532 /out/state /data
USER 65532:65532
WORKDIR /app
ENV ERASER_DATA_DIR=/data ERASER_BROKERS_FILE=/app/data/brokers.yaml ERASER_ENABLE_SEND=false
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/eraser", "healthcheck"]
ENTRYPOINT ["/eraser"]
CMD ["serve", "--listen", "0.0.0.0:8080"]
