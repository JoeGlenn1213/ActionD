# ActionD MCP server container — used by MCP registries (e.g. Glama) to
# start the server and run protocol introspection checks. The daemon itself
# runs on the host; `actiond mcp` only needs the binary to start and answer
# initialize/tools-list over stdio.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/actiond ./cmd/actiond

FROM alpine:3.20
COPY --from=build /out/actiond /usr/local/bin/actiond
# Toolchain PATH for plugins that shell out to git/python inside containers.
ENV PATH="/usr/local/bin:${PATH}"
ENTRYPOINT ["actiond", "mcp"]
