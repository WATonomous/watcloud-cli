# Single source of truth for building the `watcloud` binary.
# Both CI (release.yml, ci.yml) and local/cluster development build through this
# Dockerfile so the artifact is identical everywhere.
#
# Build the linux/amd64 binary and extract it to ./dist/watcloud:
#   docker buildx build --platform linux/amd64 --output type=local,dest=dist .
#
# The default target produces the linux/amd64 binary regardless of the host
# platform, which is what the cluster runs.

FROM --platform=$BUILDPLATFORM golang:1.24 AS build

WORKDIR /src

# Cache module downloads separately from the source build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Match the release build: statically linked, stripped, linux/amd64.
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/watcloud ./cmd/watcloud

# `--output type=local` copies everything in this scratch stage to the host.
FROM scratch AS binary
COPY --from=build /out/watcloud /watcloud
