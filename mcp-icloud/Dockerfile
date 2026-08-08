# --- build ---
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/icloud-smtp .

# --- distroless runtime ---
# :nonroot — this service writes nothing to disk, so drop root to shrink blast radius.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/icloud-smtp /usr/local/bin/icloud-smtp
ENV PORT=9001
EXPOSE 9001
ENTRYPOINT ["/usr/local/bin/icloud-smtp"]
