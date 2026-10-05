# syntax=docker/dockerfile:1
# crm-node image for the storage PC: Go binary + ffmpeg for posters and previews.
# Build from the langplant-crm directory:
#   docker build -f deploy/node/Dockerfile -t langplant-crm-node .

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X langplant-crm/internal/node.Version=${VERSION}" -o /out/crm-node ./cmd/crm-node

FROM alpine:3.22
RUN apk add --no-cache ffmpeg ca-certificates tzdata
COPY --from=build /out/crm-node /usr/local/bin/crm-node
ENV NODE_DATA_DIR=/storage
VOLUME /storage
ENTRYPOINT ["crm-node"]
