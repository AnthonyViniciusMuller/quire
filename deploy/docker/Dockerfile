# The node, as a container image.
#
# It is built in two stages so that what ships is the binary and nothing that
# produced it: no compiler, no module cache, no source. The runtime is Alpine
# rather than scratch because the node makes outbound HTTPS calls — a
# .well-known lookup against a peer — and a chain it cannot verify is a
# federation it cannot join.

FROM golang:1.27-alpine AS build

WORKDIR /src

# The module graph is copied on its own so that the download is cached against
# go.mod and go.sum, and is not repeated for a change to a source file.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is off so that the binary carries no dynamic loader, which is what lets
# the same build run on a distroless base if this image ever needs one.
RUN CGO_ENABLED=0 go build -trimpath -o /out/quired ./cmd/quired

FROM alpine:3.22

RUN apk add --no-cache ca-certificates libcap \
    && adduser --disabled-password --uid 10001 quire

COPY --from=build /out/quired /usr/local/bin/quired

# The discovery listener answers on 80, which an unprivileged user may not bind
# without being allowed to. The capability is on the binary rather than on the
# container, so the node keeps every other privilege it does not have — and it
# is this and not a root user, because a process that may bind a low port is a
# far smaller grant than a process that may do anything.
RUN setcap cap_net_bind_service=+ep /usr/local/bin/quired

USER quire

# 80 is the discovery listener and 9090 is the API. RFC 8615 puts the documents
# on a path of the domain rather than on a port of it, so a node that answers
# discovery on anything but the default port is a node no peer can find by its
# domain alone.
EXPOSE 80 9090

ENTRYPOINT ["quired"]
