# Build the binary against the full toolchain.
FROM golang:1.27-alpine AS build

WORKDIR /src

# Dependencies are copied first so this layer is cached until they change.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a static binary, which is what lets the runtime stage
# use an image with no libc. -trimpath keeps build paths out of the binary and
# -s -w drop the symbol table and DWARF data.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/gateway \
    ./cmd/gateway

# Run on a distroless base: no shell, no package manager, non-root by default.
# There is nothing in the image for an attacker who gains execution to use.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/gateway /gateway

EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/gateway"]
