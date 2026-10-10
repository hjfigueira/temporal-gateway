# ADR-016: Distroless, non-root container image with a soft memory cap

**Status:** Accepted

**Related features:** [`deployment-cicd.feature`](../../features/deployment-cicd.feature)

## Context

The gateway ships as a container image; minimizing attack surface and
image size matters for a process with no legitimate need for a shell,
package manager, or root privileges at runtime.

## Decision

`.docker/Dockerfile` is a two-stage build: `golang:1.26-alpine` to compile
a static (`CGO_ENABLED=0`), stripped (`-ldflags="-s -w"`) binary, copied
into `gcr.io/distroless/static-debian12:nonroot`. The container runs as
`nonroot:nonroot`. `GOMEMLIMIT=300MiB` is set so the Go runtime GCs more
aggressively as usage approaches that cap instead of letting RSS grow to
roughly 2x live heap under load — meant to be overridden per-deployment to
match the container's actual memory limit.

## Consequences

- The resulting image (~35MB) has no shell, so debugging a running
  container means `kubectl cp`/ephemeral debug containers, not `kubectl
  exec sh`.
- The baked-in `config.yml`/`api-spec.yaml` are example defaults; a real
  deployment mounts its own over them (documented in the README) rather
  than rebuilding the image per environment.
