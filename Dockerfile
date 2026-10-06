# Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0
#
# PUDL Desktop in a container. The store is pure Go, so the binary is
# static; the image also holds Chromium, which draws the pictures of PUDL
# Studio's canvas that an agent looks at. Documents live in /data, which
# should be a volume.
#
#   docker build -t pudl-desktop .
#   docker run -d --name pudl-desktop --restart unless-stopped \
#     -p 127.0.0.1:8200:8200 -v pudl-desktop-data:/data pudl-desktop
#
# The run origin is served on the same port under its own host name. The
# service's browser reaches it as localhost:8200, inside the container.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pudl-desktop ./cmd/pudl-desktop

FROM alpine:3.22
RUN apk add --no-cache chromium font-noto font-dejavu ca-certificates \
 && addgroup -g 65532 nonroot && adduser -D -u 65532 -G nonroot -h /home/nonroot nonroot \
 && mkdir -p /data && chown nonroot:nonroot /data
COPY --from=build /out/pudl-desktop /pudl-desktop
USER nonroot
VOLUME /data
EXPOSE 8200
ENTRYPOINT ["/pudl-desktop", "-addr", ":8200", "-data", "/data", "-chrome", "/usr/bin/chromium-browser"]
CMD ["-secure", \
     "-run-hosts", "pudl-run.parkscomputing.com,localhost:8200", \
     "-run-origin", "https://pudl-run.parkscomputing.com", \
     "-render-base", "http://localhost:8200"]
