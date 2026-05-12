# Stage 1: Build Go binaries
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v3 \
    go build -ldflags="-s -w" -o /api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v3 \
    go build -ldflags="-s -w" -o /indexer ./cmd/indexer

# Stage 2: Build the IVF index (baked into image)
FROM alpine:3.20 AS indexer
RUN apk add --no-cache ca-certificates
COPY --from=build /indexer /indexer
COPY resources/references.json.gz /resources/references.json.gz
RUN mkdir -p /data && \
    /indexer --refs /resources/references.json.gz --out /data/index.bin --clusters 1024 --iters 30

# Stage 3: Minimal final image
FROM alpine:3.20 AS final
COPY --from=build /api /api
COPY --from=indexer /data/index.bin /data/index.bin
COPY resources/mcc_risk.json /resources/mcc_risk.json
COPY resources/normalization.json /resources/normalization.json
EXPOSE 8080
ENTRYPOINT ["/api"]
