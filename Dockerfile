FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /janus ./cmd/janus

FROM alpine:3.21
RUN adduser -D -u 10001 janus
USER janus
COPY --from=build /janus /usr/local/bin/janus
EXPOSE 8200
ENTRYPOINT ["janus"]
CMD ["server"]
