FROM registry.access.redhat.com/ubi9/go-toolset:9.9-1794518352@sha256:4053e919374d739ee6b0007e412d89256b0bd065e0bc690c2b3905bbaab6500e as builder
COPY LICENSE /licenses/LICENSE
WORKDIR /build
RUN git config --global --add safe.directory /build
COPY . .
RUN CGO_ENABLED=0 go build -o datadog-prometheus-exporter -a -installsuffix cgo main.go

FROM builder as test
RUN go test ./...

FROM registry.access.redhat.com/ubi9-minimal@sha256:baafce2d34783a28b4a472779f304e97e00f9d35c99553bc53b068b14344d9a9
RUN microdnf update -y && microdnf install -y ca-certificates && rm -rf /var/cache/yum
COPY --from=builder /build/datadog-prometheus-exporter /
ENTRYPOINT ["/datadog-prometheus-exporter"]
CMD ["serve"]
