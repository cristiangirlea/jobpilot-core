FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

# The API with the claude and codex CLIs for LLM_PROVIDER=cli (docker build --target ai).
# No credentials here: the CLI's token comes from the environment at run time.
FROM alpine:3.21 AS ai
RUN apk add --no-cache nodejs npm libgcc libstdc++ ripgrep && \
    npm install -g --no-fund --no-audit @anthropic-ai/claude-code@2.1.319 @openai/codex@0.167.0 && \
    npm cache clean --force && \
    adduser -D -h /home/jobpilot -u 10001 jobpilot
ENV HOME=/home/jobpilot USE_BUILTIN_RIPGREP=0 DISABLE_AUTOUPDATER=1
WORKDIR /app
COPY --from=build /out/api /out/seed /app/
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/api"]

# The default image: no AI CLIs.
FROM alpine:3.21 AS runtime
RUN adduser -D -H -u 10001 jobpilot
WORKDIR /app
COPY --from=build /out/api /out/seed /app/
# Numeric, so Kubernetes can check runAsNonRoot.
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/api"]
