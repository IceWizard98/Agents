# --- build ---
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/icloud-smtp .

# --- distroless runtime ---
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/icloud-smtp /usr/local/bin/icloud-smtp
ENV PORT=9001
EXPOSE 9001
ENTRYPOINT ["/usr/local/bin/icloud-smtp"]
