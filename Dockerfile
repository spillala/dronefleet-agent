# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS agent-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/dronefleet-agent ./cmd/agent

# dronefleet-mcp lives in a sibling repo; Agent 1 spawns it as a stdio
# subprocess (per dronefleet-mcp's documented architecture), so it needs to
# be present in the same image rather than reached over the network.
FROM golang:1.25-bookworm AS mcp-build
RUN git clone --depth 1 https://github.com/spillala/dronefleet-mcp /src
WORKDIR /src
RUN CGO_ENABLED=0 go build -o /out/dronefleet-mcp ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=agent-build /out/dronefleet-agent /usr/local/bin/dronefleet-agent
COPY --from=mcp-build /out/dronefleet-mcp /usr/local/bin/dronefleet-mcp

ENV DRONEFLEET_MCP_PATH=/usr/local/bin/dronefleet-mcp
ENTRYPOINT ["dronefleet-agent"]
