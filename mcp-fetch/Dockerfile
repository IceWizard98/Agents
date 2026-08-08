# --- build ---
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mcp-fetch .

# --- distroless runtime ---
# :nonroot — this service writes nothing to disk, so drop root to shrink blast radius.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mcp-fetch /usr/local/bin/mcp-fetch
ENV PORT=9002
EXPOSE 9002
ENTRYPOINT ["/usr/local/bin/mcp-fetch"]
