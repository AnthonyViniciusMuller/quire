# The node, as a container image.
#
# It is built in stages so that what ships is the binary and nothing that
# produced it: no compiler, no module cache, no source. There are two runtime
# stages and they answer different deployments, which is why neither of them is
# a compromise between the two.
#
#   --target compose (the default) is Alpine. It is what deploy/docker/compose
#   runs, and it is Alpine for one reason: RFC 8615 puts the discovery documents
#   on a path of the domain, a domain carries no port, so a node found by its
#   domain alone has to answer on 80 — and binding 80 as an unprivileged user
#   needs a capability set on the binary, which needs libcap to set.
#
#   --target cluster is distroless: no shell, no package manager, no libcap, and
#   nothing to bind 80 with. It does not need to. In a cluster the address a
#   peer resolves belongs to the gateway, which listens on 443 and 80 and
#   forwards to this pod on 8080, so the port the process binds is an
#   implementation detail of the pod and never the one the federation dials.
#   That is what makes the smaller base possible rather than merely desirable:
#   the one privilege the laptop image needs is the one the cluster does not.
#
# Both carry the same binary from the same stage, so the two deployments run the
# same build and not two of them.

FROM golang:1.27-alpine AS build

WORKDIR /src

# The module graph is copied on its own so that the download is cached against
# go.mod and go.sum, and is not repeated for a change to a source file.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is off so that the binary carries no dynamic loader, which is what lets
# the same build run on a distroless base.
RUN CGO_ENABLED=0 go build -trimpath -o /out/quired ./cmd/quired

# --- the laptop image --------------------------------------------------------

FROM alpine:3.22 AS compose

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

# 80 is the discovery listener and 9090 is the API.
EXPOSE 80 9090

ENTRYPOINT ["quired"]

# --- the cluster image -------------------------------------------------------

# static rather than base: the binary is built with CGO off, so it needs no libc
# at all, and what this base adds over scratch is the certificate bundle the
# node verifies a peer's .well-known lookup against — a chain it cannot verify
# is a federation it cannot join — plus the passwd entry that makes `nonroot` a
# user and not just a number, and the zoneinfo a timestamp is rendered against.
FROM gcr.io/distroless/static-debian12:nonroot AS cluster

# The labels are read by the registry, by the cluster and by whoever is holding
# the pager: an image with no source annotation is one nobody can get back from
# a running pod to the commit that built it. The two that vary are filled in by
# the build; the rest are the same for every image this repository produces.
ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="quired" \
      org.opencontainers.image.description="A Quire federation node." \
      org.opencontainers.image.source="https://github.com/anthonyvsmuller/quire" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.base.name="gcr.io/distroless/static-debian12:nonroot" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

COPY --from=build /out/quired /usr/local/bin/quired

# 65532 is distroless's own nonroot user, and it is named rather than numbered
# so that the manifests can say runAsNonRoot without also having to repeat which
# non-root user this image happens to use.
USER nonroot:nonroot

# 8080 is the discovery, health and metrics listener and 9090 is the API.
# Neither is privileged, which is what lets the pod drop every capability
# including the one the laptop image needs.
EXPOSE 8080 9090

ENTRYPOINT ["/usr/local/bin/quired"]

# --- the schema --------------------------------------------------------------

# The migrations, as an image the cluster can run.
#
# On a laptop the compose file mounts the directory, which is right there: the
# files are on the machine that is running them, and editing one and restarting
# is the whole loop. A cluster has neither the directory nor a way to reach it,
# and the two alternatives are worse than a five-line image. A ConfigMap holds
# a megabyte, which the schema does not exceed today and is not a bound anybody
# should have to remember; and it can only be generated from files under the
# kustomization root, which the schema is not and should not have to be moved
# under.
#
# What this buys beyond that is that the schema is versioned with the binary
# that expects it: one tag names both, and a node deployed from a commit runs
# the migrations of that commit rather than of whatever the cluster last
# applied.
FROM migrate/migrate:v4.19.1 AS migrate

ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="quire-migrate" \
      org.opencontainers.image.description="The Quire schema, applied by golang-migrate." \
      org.opencontainers.image.source="https://github.com/anthonyvsmuller/quire" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

COPY migrations /migrations
