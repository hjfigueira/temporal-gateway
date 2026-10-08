# Builder stage for rr-custom (rrbuild/) - notification-service's own
# RoadRunner build: the same temporal/http/rpc/informer/resetter plugins
# the stock ./rr binary bundled, plus cascade_filter, a plugin that exists
# nowhere in RoadRunner's own prebuilt binaries - see rrbuild/main.go and
# rrbuild/cascadefilter/plugin.go for why. CGO_ENABLED=0 statically links
# it, so the final stage below needs no Go toolchain of its own, just the
# resulting binary.
FROM golang:1.26 AS rrbuild
WORKDIR /rrbuild
COPY rrbuild/go.mod rrbuild/go.sum ./
RUN go mod download
COPY rrbuild/ ./
RUN CGO_ENABLED=0 go build -o /rr-custom .

# notification-service needs ext-grpc for the Temporal PHP SDK's Client
# component (used in http-worker.php to start NotificationWorkflow) - the
# pure-PHP grpc/grpc composer package alone isn't enough; BaseClient::create
# hard-requires the native extension at runtime. Compiling ext-grpc from
# source (via pecl) takes a long time, so this uses a base image that
# already ships it prebuilt - see
# https://hub.docker.com/r/beabys/php-grpc.
FROM beabys/php-grpc:8.4.3-fpm-grpc

# unzip is how Composer extracts packages (no ext-zip in this image); git
# covers any VCS-sourced dependency; sockets is a core PHP extension
# spiral/roadrunner-worker requires that this image doesn't build in -
# unlike grpc, it's bundled with the PHP source, so this is a quick local
# build (build tools are already present in this image), not a slow
# from-scratch compile.
RUN apt-get update && apt-get install -y --no-install-recommends \
        git unzip \
    && docker-php-ext-install sockets \
    && rm -rf /var/lib/apt/lists/*

COPY --from=composer:2 /usr/bin/composer /usr/bin/composer

WORKDIR /app
COPY composer.json ./
RUN composer install --no-interaction --no-dev --optimize-autoloader

COPY . .
COPY --from=rrbuild /rr-custom ./rr-custom
RUN chmod +x rr-custom

# host.docker.internal resolves to the Docker host on Docker Desktop
# automatically; on Linux, pass `--add-host=host.docker.internal:host-gateway`
# (same convention the root README's own Docker instructions use). Override
# with `-e TEMPORAL_HOST=...` to join the Temporal server's own Docker
# network instead.
ENV TEMPORAL_HOST=host.docker.internal:7233

EXPOSE 8083
CMD ["./rr-custom"]
