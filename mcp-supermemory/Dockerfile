# --- build ---
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mcp-supermemory .

# --- distroless runtime ---
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/mcp-supermemory /usr/local/bin/mcp-supermemory
ENV PORT=9003
EXPOSE 9003
ENTRYPOINT ["/usr/local/bin/mcp-supermemory"]
