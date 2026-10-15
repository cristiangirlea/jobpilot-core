FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

FROM alpine:3.21
RUN adduser -D -H -u 10001 jobpilot
WORKDIR /app
COPY --from=build /out/api /out/seed /app/
# Numeric, so Kubernetes can check runAsNonRoot.
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/api"]

