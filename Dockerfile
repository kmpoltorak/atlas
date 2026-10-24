# ONNX Runtime's official Linux releases are glibc-built (manylinux baseline); Alpine's musl
# libc can't safely dlopen them (mixing a musl-linked process with a glibc .so is a real crash
# risk, not just a compatibility warning — see docs/ml.md). This is also the repo's first cgo
# dependency (github.com/yalue/onnxruntime_go, needed to dlopen libonnxruntime at runtime), so
# both stages moved off Alpine to a consistent glibc toolchain end to end.
FROM golang:1.27.3-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY docs ./docs
# The SQL files are embedded into the binary (migrations/embed.go), so the API applies them
# itself at startup rather than relying on Postgres's docker-entrypoint-initdb.d hook, which only
# ever runs against a brand-new data volume. That means they have to be present at build time.
COPY migrations ./migrations

# No GOARCH: the builder runs on the image's target platform (linux/amd64 or linux/arm64, from
# `docker build --platform` or the host), and cgo compiles for the architecture it runs on. A
# hardcoded amd64 made every Apple Silicon or ARM server run the whole image under emulation.
RUN CGO_ENABLED=1 go build -o /out/atlas-api ./cmd/api

# Fetches the native onnxruntime shared library into its own stage so the final image only
# copies the handful of files it needs, not the full SDK (headers, static libs, docs) the
# release tarball ships.
# Supported architectures are exactly the two below; anything else fails the build here rather
# than shipping an image whose native library cannot load.
FROM debian:bookworm-slim AS onnxruntime
ARG ONNXRUNTIME_VERSION=1.28.0
ARG TARGETARCH
RUN case "${TARGETARCH}" in \
      amd64) ORT_ARCH=x64 ;; \
      arm64) ORT_ARCH=aarch64 ;; \
      *) echo "unsupported architecture: ${TARGETARCH} (amd64 and arm64 are supported)" >&2; exit 1 ;; \
    esac \
    && apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && curl -fsSL -o /tmp/ort.tgz \
       "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-${ORT_ARCH}-${ONNXRUNTIME_VERSION}.tgz" \
    && mkdir -p /onnxruntime \
    && tar -xzf /tmp/ort.tgz -C /onnxruntime --strip-components=1 \
    && rm /tmp/ort.tgz \
    && rm -rf /var/lib/apt/lists/*

FROM debian:bookworm-slim

# libstdc++6/libgomp1: onnxruntime's CPU execution provider links against both; bookworm-slim's
# --no-install-recommends base doesn't guarantee libgomp1 is already present. curl: needed by
# docker-compose.yml's healthcheck (a plain "exec wget ..." — this image never had wget, which
# was a real, live bug: `docker inspect` showed this container reporting "unhealthy" nonstop
# despite /health genuinely returning 200, since every healthcheck exec failed with "wget:
# executable file not found".
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl libstdc++6 libgomp1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --no-create-home --shell /usr/sbin/nologin --uid 10001 appuser

WORKDIR /app

COPY --from=onnxruntime /onnxruntime/lib/libonnxruntime.so* /usr/lib/
COPY --from=builder /out/atlas-api /app/atlas-api

# Numeric UID, not the username: a runtime that enforces non-root (Kubernetes' runAsNonRoot, for
# one) checks the image's USER without reading /etc/passwd, and rejects a named user outright.
USER 10001

EXPOSE 8080

# LIBONNXRUNTIME_PATH matches where the .so landed above. MODEL_PATH is left unset here — an
# absent model is a normal, non-fatal startup state (see internal/application/ml); mount/copy a
# real trained model and set MODEL_PATH to enable real inference instead of the RSI-derived
# stub fallback (see training/README.md).
ENV LIBONNXRUNTIME_PATH=/usr/lib/libonnxruntime.so

CMD ["/app/atlas-api"]
