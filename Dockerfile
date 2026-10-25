FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ruche-server ./cmd/ruche-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ruche-server /ruche-server
ENV RUCHE_DB=/data/ruche.db RUCHE_LISTEN=:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/ruche-server"]
