# syntax=docker/dockerfile:1
# crm-server image for the VPS: ~25 MB, runs as an unprivileged user.
# Build from the langplant-crm directory:
#   docker build -f deploy/server/Dockerfile -t langplant-crm-server .

FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN mkdir -p ../internal/webui/dist && npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X langplant-crm/internal/server.Version=${VERSION}" -o /out/crm-server ./cmd/crm-server

# The binary embeds time zone data and makes no outbound TLS calls, so the
# bare alpine base is enough (busybox wget is used by the health check).
FROM alpine:3.22
RUN adduser -D -H -u 10001 crm && mkdir /data && chown crm:crm /data
COPY --from=build /out/crm-server /usr/local/bin/crm-server
ENV CRM_ADDR=:8080 CRM_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER crm
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1
ENTRYPOINT ["crm-server"]
