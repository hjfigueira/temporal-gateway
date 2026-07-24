# syntax=docker/dockerfile:1

# --- build ---
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/temporal-gateway .

# --- run ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/temporal-gateway ./temporal-gateway
COPY config.yml api-spec.yaml ./

USER nonroot:nonroot
EXPOSE 8081

ENTRYPOINT ["./temporal-gateway"]
