FROM golang:1.27.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/web ./cmd/web && \
    CGO_ENABLED=0 go build -trimpath -o /out/storage ./cmd/storage && \
    CGO_ENABLED=0 go build -trimpath -o /out/admin ./cmd/admin

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --uid 10001 --create-home tritontube && \
    mkdir /data && chown tritontube:tritontube /data
COPY --from=build /out/ /usr/local/bin/
USER tritontube
WORKDIR /data
EXPOSE 8080 9001
CMD ["web", "-listen", ":8080", "-db", "/data/metadata.db", "-work", "/data/tmp"]
