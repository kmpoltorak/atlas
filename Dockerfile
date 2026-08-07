# ONNX Runtime's official Linux releases are glibc-built (manylinux baseline); Alpine's musl
# libc can't safely dlopen them (mixing a musl-linked process with a glibc .so is a real crash
# risk, not just a compatibility warning — see docs/ml.md). This is also the repo's first cgo
# dependency (github.com/yalue/onnxruntime_go, needed to dlopen libonnxruntime at runtime), so
# both stages moved off Alpine to a consistent glibc toolchain end to end.
FROM golang:1.25-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o /out/atlas-api ./cmd/api

# Fetches the native onnxruntime shared library into its own stage so the final image only
# copies the handful of files it needs, not the full SDK (headers, static libs, docs) the
# release tarball ships.
FROM debian:bookworm-slim AS onnxruntime
ARG ONNXRUNTIME_VERSION=1.28.0
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && curl -fsSL -o /tmp/ort.tgz \
       "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz" \
    && mkdir -p /onnxruntime \
    && tar -xzf /tmp/ort.tgz -C /onnxruntime --strip-components=1 \
    && rm /tmp/ort.tgz \
    && rm -rf /var/lib/apt/lists/*

FROM debian:bookworm-slim

# libstdc++6/libgomp1: onnxruntime's CPU execution provider links against both; bookworm-slim's
# --no-install-recommends base doesn't guarantee libgomp1 is already present.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates libstdc++6 libgomp1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --no-create-home --shell /usr/sbin/nologin --uid 10001 appuser

WORKDIR /app

COPY --from=onnxruntime /onnxruntime/lib/libonnxruntime.so* /usr/lib/
COPY --from=builder /out/atlas-api /app/atlas-api

# Numeric UID, not the username: Kubernetes' runAsNonRoot pod security check can't resolve
# /etc/passwd inside the image before starting the container, so it requires the image's own
# USER to already be numeric — confirmed live on a real cluster (minikube), where "USER appuser"
# made every pod fail with "cannot verify user is non-root" and never start, a failure mode
# Docker Compose never surfaces since it doesn't enforce runAsNonRoot at all.
USER 10001

EXPOSE 8080

# LIBONNXRUNTIME_PATH matches where the .so landed above. MODEL_PATH is left unset here — an
# absent model is a normal, non-fatal startup state (see internal/application/ml); mount/copy a
# real trained model and set MODEL_PATH to enable real inference instead of the RSI-derived
# stub fallback (see training/README.md).
ENV LIBONNXRUNTIME_PATH=/usr/lib/libonnxruntime.so

CMD ["/app/atlas-api"]
