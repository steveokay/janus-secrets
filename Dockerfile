# --- web build stage ---
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- go build stage ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist/ ./internal/web/dist/
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /janus ./cmd/janus

FROM alpine:3.21
RUN adduser -D -u 10001 janus
USER janus
COPY --from=build /janus /usr/local/bin/janus
EXPOSE 8200
ENTRYPOINT ["janus"]
CMD ["server"]
