FROM registry.access.redhat.com/ubi9/go-toolset:9.8-1793294290@sha256:cab9089469c52029e4d8a4e9f717e87c51ab22eed448ebcada101b811f97dfe7 as builder
COPY LICENSE /licenses/LICENSE
WORKDIR /build
RUN git config --global --add safe.directory /build
COPY . .
RUN CGO_ENABLED=0 go build -o datadog-prometheus-exporter -a -installsuffix cgo main.go

FROM builder as test
RUN go test ./...

FROM registry.access.redhat.com/ubi9-minimal@sha256:bba2b4bbf0ed15cfe7dd72a1e17ae7d2015a141f306b304e1a2785c41400e39c
RUN microdnf update -y && microdnf install -y ca-certificates && rm -rf /var/cache/yum
COPY --from=builder /build/datadog-prometheus-exporter /
ENTRYPOINT ["/datadog-prometheus-exporter"]
CMD ["serve"]
