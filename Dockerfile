# syntax=docker/dockerfile:1.7

FROM node:20-alpine AS web-build
WORKDIR /src

RUN corepack enable

COPY package.json pnpm-workspace.yaml tsconfig.base.json ./
COPY apps/ptrack-web/package.json apps/ptrack-web/package.json
COPY packages/ptrack-components/package.json packages/ptrack-components/package.json

RUN pnpm install --no-frozen-lockfile

COPY apps ./apps
COPY packages ./packages

RUN pnpm build

FROM golang:1.24-alpine AS go-build
WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/ptrack ./cmd/ptrack

FROM alpine:3.20
WORKDIR /app

RUN apk add --no-cache ca-certificates

COPY --from=go-build /out/ptrack /app/ptrack
COPY --from=web-build /src/apps/ptrack-web/dist /app/web

ENV PTRACK_HTTP_ADDRESS=0.0.0.0:7777 \
    PTRACK_SOCKET_PATH=/tmp/ptrack.sock \
    PTRACK_WEB_DIR=/app/web

EXPOSE 7777

ENTRYPOINT ["/app/ptrack"]
CMD ["serve"]
