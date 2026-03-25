# Stage 1: build
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Download dependencies first (cached layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a fully static binary.
# Pass VERSION, COMMIT, and BUILD_TIME as build-args so that the binary
# embeds release metadata at link time.  Defaults produce a "dev" build.
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=""
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w \
      -X github.com/RRussell11/AIISTECH-Backend/internal/version.Version=${VERSION} \
      -X github.com/RRussell11/AIISTECH-Backend/internal/version.Commit=${COMMIT} \
      -X github.com/RRussell11/AIISTECH-Backend/internal/version.BuildTime=${BUILD_TIME}" \
    -o /bin/server ./cmd/server

# Stage 2: minimal runtime image (distroless)
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/server /server

EXPOSE 8080

ENTRYPOINT ["/server"]