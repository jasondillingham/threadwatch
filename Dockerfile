# syntax=docker/dockerfile:1.7

# ─── build stage ─────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS build

WORKDIR /src

# Cache module downloads independent of source changes.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Build args populated by CI to surface build metadata via the binary.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

ENV CGO_ENABLED=0

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build \
      -trimpath \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.buildDate=${BUILD_DATE}" \
      -o /out/threadwatch \
      ./cmd/threadwatch

# ─── runtime stage ───────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/threadwatch /usr/local/bin/threadwatch

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/threadwatch"]
