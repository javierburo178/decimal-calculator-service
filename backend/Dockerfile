# Build stage: compile a static binary with the Go toolchain.
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bin/calculator .

# Runtime stage: minimal image, no toolchain, non-root.
FROM alpine:3.20
RUN adduser -D -u 10001 appuser
COPY --from=build /bin/calculator /usr/local/bin/calculator
USER appuser
EXPOSE 8080
ENTRYPOINT ["calculator"]
