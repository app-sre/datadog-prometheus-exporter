FROM registry.access.redhat.com/ubi9/go-toolset:9.8-1793107762@sha256:c8947b60e6568dcdc5f66d382e18fa1fb89039918f59783e45b8c90847579955 as builder
COPY LICENSE /licenses/LICENSE
WORKDIR /build
RUN git config --global --add safe.directory /build
COPY . .
RUN CGO_ENABLED=0 go build -o datadog-prometheus-exporter -a -installsuffix cgo main.go

FROM builder as test
RUN go test ./...

FROM registry.access.redhat.com/ubi9-minimal@sha256:6f3a78179e0b30b930a186e7f68a4af52e51bb82406c9653be10bddd624e40f5
RUN microdnf update -y && microdnf install -y ca-certificates && rm -rf /var/cache/yum
COPY --from=builder /build/datadog-prometheus-exporter /
ENTRYPOINT ["/datadog-prometheus-exporter"]
CMD ["serve"]
