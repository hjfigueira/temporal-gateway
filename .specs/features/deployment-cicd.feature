# See .specs/adr/active/0016-distroless-non-root-container-image.md
# See .specs/adr/active/0017-release-triggered-multi-arch-image-publish-to-ghcr.md
# See .specs/adr/active/0024-ci-checks-on-every-push-and-pr.md
# Code: .docker/Dockerfile, .github/workflows/docker-release.yml,
#       .github/workflows/ci.yml

Feature: Container image and release publishing
  The gateway ships as a small, non-root, distroless container image, built
  and published to GHCR automatically whenever a GitHub Release is
  published - never on arbitrary commits.

  Scenario: The built image runs as a non-root user
    Given the image built from .docker/Dockerfile
    When a container starts from it
    Then the process runs as "nonroot:nonroot", not root

  Scenario: The image has no shell or package manager
    Given the runtime stage is gcr.io/distroless/static-debian12:nonroot
    When someone tries to "docker exec sh" into a running container
    Then there is no shell available to exec into

  Scenario: GOMEMLIMIT gives the Go runtime a soft memory cap
    Given the image sets ENV GOMEMLIMIT=300MiB
    When the process runs under sustained load
    Then the Go GC runs more aggressively as usage approaches 300MiB
    # Instead of letting RSS grow to roughly 2x live heap
    And this value is expected to be overridden per-deployment to match the container's actual memory limit

  Scenario: The baked-in config/spec are examples, not production config
    Given the image COPYs config.yml and api-spec.yaml as defaults
    When deploying to a real environment
    Then the operator mounts their own config.yml/api-spec.yaml over the baked-in ones rather than rebuilding the image per environment

  Scenario: Publishing happens only when a GitHub Release is published
    Given no GitHub Release has been published
    When commits land on any branch, including main
    Then no image is built or pushed to GHCR

  Scenario: A published Release triggers a multi-arch build and push
    Given a GitHub Release "v1.2.3" is published (not a prerelease)
    When the docker-release workflow runs
    Then images for linux/amd64 and linux/arm64 are built
    And pushed to ghcr.io/<owner>/temporal-gateway tagged "1.2.3", "1.2", and "latest"

  Scenario: A prerelease never updates the "latest" tag
    Given a GitHub Release "v2.0.0-rc.1" is published and marked prerelease
    When the docker-release workflow runs
    Then images are tagged "2.0.0-rc.1" only
    But neither "2.0" nor "latest" is updated to point at it
    # docker/metadata-action emits only {{version}} for prerelease versions.

  Scenario: No extra registry secrets are required
    Given the workflow logs in to GHCR
    When it authenticates
    Then it uses the built-in GITHUB_TOKEN
    And no additional repository secret needs to be configured

  Scenario: Every push and pull request to main runs the checks
    Given a push to main or a pull request against main
    When the ci workflow runs
    Then it fails if "gofmt -s -l ." lists any file
    And it runs "go vet ./..." and "go test -race ./..."
    And it uses the Go version declared in go.mod

  Scenario: CI fails on a reachable known vulnerability
    Given a dependency or the Go toolchain has a published advisory
    And our code calls the vulnerable symbol
    When the ci workflow runs govulncheck
    Then the check fails naming the advisory and the fixed version
    But an advisory in code we never call does not fail the build
