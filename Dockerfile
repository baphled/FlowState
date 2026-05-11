# syntax=docker/dockerfile:1.7

FROM node:22-bookworm-slim AS web-build
WORKDIR /src/web

COPY web/package*.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS go-build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web-build /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/flowstate ./cmd/flowstate

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=go-build /out/flowstate /app/flowstate
COPY --from=web-build /src/web/dist /app/web/dist

ENV FLOWSTATE_WEB_DIST_DIR=/app/web/dist
ENV PORT=10000
EXPOSE 10000

CMD ["/bin/sh", "-c", "/app/flowstate serve --host 0.0.0.0 --port ${PORT:-10000}"]
