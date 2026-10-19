FROM golang:1.26.2-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY assets/ assets/
COPY config.example.yaml template.go ./

RUN CGO_ENABLED=0 go build -trimpath -o /out/opskit ./cmd/opskit \
    && CGO_ENABLED=0 go test -c -o /out/opskit-app.test ./internal/app

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates bash curl python3-yaml \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/opskit /usr/local/bin/opskit
COPY --from=build /out/opskit-app.test /usr/local/bin/opskit-app.test
COPY config.example.yaml /etc/opskit/config.example.yaml
COPY tests/e2e/docker/generate-install-config.py /usr/local/bin/opskit-e2e-generate-config
COPY tests/e2e/docker/install-entrypoint.sh /usr/local/bin/opskit-e2e-install
COPY tests/e2e/docker/tester-entrypoint.sh /usr/local/bin/opskit-e2e-tester
RUN chmod 0755 /usr/local/bin/opskit-e2e-tester /usr/local/bin/opskit-e2e-install /usr/local/bin/opskit-e2e-generate-config

ENTRYPOINT ["/usr/local/bin/opskit-e2e-tester"]
