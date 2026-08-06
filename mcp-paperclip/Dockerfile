# --- build ---
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/paperclip-control .

# --- distroless runtime ---
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/paperclip-control /usr/local/bin/paperclip-control
ENV PORT=9500
EXPOSE 9500
ENTRYPOINT ["/usr/local/bin/paperclip-control"]
