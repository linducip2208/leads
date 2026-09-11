FROM golang:1.26.2-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/scheduler ./cmd/scheduler \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/benchmark-search ./cmd/benchmark-search

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates chromium \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --uid 10001 leadforge
WORKDIR /app
COPY --from=build /out/ /app/bin/
COPY db/migrations /app/db/migrations
COPY static /app/static
USER leadforge
ENV APP_ENV=production \
    APP_ADDR=:8080 \
    BROWSER_ENABLED=true
EXPOSE 8080
ENTRYPOINT ["/app/bin/server"]
