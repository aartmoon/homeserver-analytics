FROM golang:1.26.8-bookworm@sha256:dc9ad6c05acc7a88e5b71bde60a5fe3bd4b9f0db209011711b464107438a8107 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/serverbot ./cmd/serverbot && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/container-collector ./cmd/container-collector && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/docker-read-proxy ./cmd/docker-read-proxy
FROM gcr.io/distroless/base-debian12:nonroot@sha256:7f0c72cd138b442ae0deeb69c08b1acf5525439ba251a49ad93c320a061567e5 AS serverbot
COPY --from=build /out/serverbot /app/serverbot
USER 1000:1000
ENTRYPOINT ["/app/serverbot"]
FROM gcr.io/distroless/base-debian12:nonroot@sha256:7f0c72cd138b442ae0deeb69c08b1acf5525439ba251a49ad93c320a061567e5 AS collector
COPY --from=build /out/container-collector /app/container-collector
USER 1000:1000
ENTRYPOINT ["/app/container-collector"]
FROM gcr.io/distroless/base-debian12:nonroot@sha256:7f0c72cd138b442ae0deeb69c08b1acf5525439ba251a49ad93c320a061567e5 AS docker-proxy
COPY --from=build /out/docker-read-proxy /app/docker-read-proxy
USER 1000:1000
ENTRYPOINT ["/app/docker-read-proxy"]
