FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# Cache mounts keep downloaded modules and compiled packages between image
# builds; they never end up in the image.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/scc ./cmd/scc

# The frontend (T15), served by the api from WEB_DIR.
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# alpine (not distroless) so compose healthchecks can use busybox wget.
FROM alpine:3.20
RUN adduser -D -u 10001 scc
COPY --from=build /out/scc /usr/local/bin/scc
COPY --from=web /web/dist /srv/web
USER scc
ENTRYPOINT ["scc"]
