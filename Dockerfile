# Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0
#
# PUDL Desktop in a container. The store is pure Go, so the binary is
# static and the image holds nothing else. Documents live in /data, which
# should be a volume.
#
#   docker build -t pudl-desktop .
#   docker run -d --name pudl-desktop --restart unless-stopped \
#     -p 127.0.0.1:8200:8200 -v pudl-desktop-data:/data pudl-desktop

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pudl-desktop ./cmd/pudl-desktop \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/pudl-desktop /pudl-desktop
COPY --from=build --chown=nonroot:nonroot /out/data /data
VOLUME /data
EXPOSE 8200
ENTRYPOINT ["/pudl-desktop", "-addr", ":8200", "-data", "/data"]
CMD ["-secure"]
