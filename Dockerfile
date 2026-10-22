FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/scc ./cmd/scc

# alpine (not distroless) so compose healthchecks can use busybox wget.
FROM alpine:3.20
RUN adduser -D -u 10001 scc
COPY --from=build /out/scc /usr/local/bin/scc
USER scc
ENTRYPOINT ["scc"]
