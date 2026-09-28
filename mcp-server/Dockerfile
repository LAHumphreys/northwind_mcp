# syntax=docker/dockerfile:1
FROM docker.io/library/golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mcp-northwind-server .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mcp-northwind-server /mcp-northwind-server
EXPOSE 8080
ENTRYPOINT ["/mcp-northwind-server"]
