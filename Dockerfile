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
RUN go mod download
COPY . .
COPY --from=frontend-builder /app/cmd/any-llm/web/dist ./cmd/any-llm/web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o any-llm ./cmd/any-llm/

# Stage 3: Minimal runtime
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
COPY --from=backend-builder /app/any-llm /usr/local/bin/any-llm
EXPOSE 8080
CMD ["any-llm"]
