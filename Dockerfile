# syntax=docker/dockerfile:1
FROM node:24-alpine AS web
RUN npm install -g pnpm@11.28.5
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.25-alpine AS api
WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.21
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=api /out/server /app/server
COPY --from=web /src/web/dist /app/web
RUN mkdir -p /app/data && chown -R app:app /app
ENV ADDR=:8080 \
    WEB_DIR=/app/web \
    DATA_DIR=/app/data
USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/health || exit 1
CMD ["/app/server"]
