FROM registry.access.redhat.com/ubi9/go-toolset:9.9-1793884566@sha256:a2ad457dc7525635613d2355037239ca9bf4374ba3b59961d1cdaa4d5f1af7d8 as builder
COPY LICENSE /licenses/LICENSE
WORKDIR /build
RUN git config --global --add safe.directory /build
COPY . .
RUN CGO_ENABLED=0 go build -o datadog-prometheus-exporter -a -installsuffix cgo main.go

FROM builder as test
RUN go test ./...

FROM registry.access.redhat.com/ubi9-minimal@sha256:f26ef64172ea61f5ac5ba4be8c5c738bf8760b22b3e5148ce7b40f7308ab32db
RUN microdnf update -y && microdnf install -y ca-certificates && rm -rf /var/cache/yum
COPY --from=builder /build/datadog-prometheus-exporter /
ENTRYPOINT ["/datadog-prometheus-exporter"]
CMD ["serve"]
