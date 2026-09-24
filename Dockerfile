# Stage 1: Build Vue frontend
FROM node:22-alpine AS frontend-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:alpine AS backend-builder
WORKDIR /app
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY . .
COPY --from=frontend-builder /app/cmd/any-llm/web/dist ./cmd/any-llm/web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o any-llm ./cmd/any-llm/

# Stage 3: Minimal runtime
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
COPY --from=backend-builder /app/any-llm /usr/local/bin/any-llm
# Metadata only: the app listens on ANY_LLM_PORT (default 6718).
EXPOSE 6718
# Keep the container clock in local time: timestamps, log rotation and the daily
# quota windows are all server-local, and a container without TZ is plain UTC.
# Override at runtime with e.g. `-e TZ=UTC`.
ENV TZ=Asia/Shanghai
CMD ["any-llm"]
