# Stage 1: Build CSS with Tailwind
FROM node:22-alpine AS css-builder
WORKDIR /build

COPY package.json package-lock.json ./
RUN npm ci

COPY web ./web
RUN npx @tailwindcss/cli -i ./web/static/css/input.css -o ./web/static/css/output.css --minify

# Stage 2: Build Go Binary (Pure-Go SQLite, no CGO/gcc required)
FROM golang:1.25-alpine AS go-builder
WORKDIR /build

RUN go install github.com/a-h/templ/cmd/templ@v0.3.977

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=css-builder /build/web/static/css/output.css ./web/static/css/output.css

RUN templ generate
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o wledger ./cmd/server

# Stage 3: Minimal Non-Root Runtime
FROM alpine:3.21
WORKDIR /wledger

RUN apk add --no-cache ca-certificates tzdata curl \
	&& addgroup -S -g 10001 wledger \
	&& adduser -S -D -H -u 10001 -G wledger wledger

COPY --from=go-builder --chown=wledger:wledger /build/wledger ./wledger

# Ensure writable data, uploads, and runtime log directories exist for non-root user
# (static assets, locales, and SQL migrations are embedded directly inside the binary)
RUN mkdir -p /wledger/data /wledger/app/uploads /wledger/app/logs \
	&& chown -R wledger:wledger /wledger

USER wledger

EXPOSE 8080

VOLUME ["/wledger/data", "/wledger/app/uploads"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD sh -c 'port="${WLEDGER_PORT:-8080}"; port="${port##*:}"; if [ -n "${WLEDGER_TLS_CERT:-}${WLEDGER_TLS_KEY:-}" ] || [ "${WLEDGER_TLS_SELF_SIGNED:-false}" = "true" ]; then curl -fsSLk "https://127.0.0.1:${port}/login" >/dev/null; else curl -fsSL "http://127.0.0.1:${port}/login" >/dev/null; fi'

CMD ["./wledger"]
