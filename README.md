# Datadog Prometheus exporter

Datadog Prometheus exporter provides a Prometheus scrape target for Datadog metrics.

It polls the [Datadog Metrics API](https://docs.datadoghq.com/api/latest/metrics/) on a configurable interval and offers the extracted data as a standard Prometheus scrape target.

Datadog Prometheus exporter can be used to bring Datadog-ingested data into existing Prometheus environments, allowing common dashboarding and alerting rules to be applied. It is the Datadog-sourced counterpart to [signalfx-prometheus-exporter](https://github.com/app-sre/signalfx-prometheus-exporter) — see [Architecture](#architecture) for why the two exporters work differently despite solving the same problem.

## Configuration
Datadog Prometheus exporter is configured via a configuration file and commandline flags.

The configuration file declares how data is read from Datadog and how it is processed into scrapable Prometheus metrics.

Each data flow is described as a Datadog [metrics query](https://docs.datadoghq.com/metrics/advanced-filtering/). Such a query yields one or more grouped time series.

The metrics provided by the `query` are translated to Prometheus compatible metrics. The `prometheusMetricTemplate` section of a `flow` supports [go templates](https://pkg.go.dev/text/template) to dynamically build Prometheus metric metadata from Datadog tags.

```yaml
dd:
  apiKey: $apiKey
  appKey: $appKey
flows:
- name: catchpoint-success-rate
  query: "avg:catchpoint.success.rate{*} by {testname}"
  pollInterval: 60s
  lookback: 5m
  prometheusMetricTemplate:
    name: catchpoint_success_rate
    type: gauge
    labels:
      probe: '{{ .DatadogTags.testname }}'
```

**Important:** a Datadog query must include a `by {tag}` grouping clause for any tag you reference in `prometheusMetricTemplate` — without it, Datadog returns a single ungrouped series with no tags at all, and any label template referencing a tag will silently render as `<no value>` rather than failing loudly. Always include every tag you plan to use as a Prometheus label in the query's `by {...}` clause.

The exporter process needs to be restarted for configuration changes to become effective.

Have a look at the [examples directory](/examples) for inspiration.

### Credentials

`dd.apiKey` and `dd.appKey` are a Datadog [API key and application key](https://docs.datadoghq.com/account_management/api-app-keys/) pair. The application key must have the `timeseries_query` scope — the more commonly granted `metrics_read` scope only covers metric/tag metadata (search, tag catalog), not reading actual data points.

`dd.site` is optional and defaults to `datadoghq.com`; set it for other Datadog sites (e.g. `datadoghq.eu`, `us3.datadoghq.com`).

## Running this software
Datadog Prometheus Exporter is available as container image.

```bash
docker run -d --rm -p 9091:9091 --name ddpe -v `pwd`:/config quay.io/app-sre/datadog-prometheus-exporter:latest serve --config /config/config.yaml
```

Visiting [http://localhost:9091/metrics](http://localhost:9091/metrics) will show the metrics as defined in the configuration file.

Observability metrics for the exporter itself are available on http://localhost:9090/metrics

## Architecture
Datadog Prometheus exporter bridges the gap between Datadog's pull-based Metrics API and Prometheus' own pull based data collection approach.

Unlike SignalFx, which offers [SignalFlow](https://dev.splunk.com/observability/docs/signalflow/) as a streaming/push data processing language, Datadog's Metrics API (`/api/v2/query/timeseries`) is query-only — there is no equivalent streaming subscription. So instead of maintaining a long-lived subscription per flow like `signalfx-prometheus-exporter` does, this exporter runs an independent polling loop per flow: on each tick it queries the configured window (`lookback`, ending now) and updates the in-memory Prometheus registry with the latest value per returned series.

Because polling re-reads a sliding window rather than receiving a one-shot event stream, picking the most recent value per series means walking the returned window backwards and using the last non-null point — Datadog commonly leaves the most recent bucket(s) incomplete until the next ingestion cycle.

## Metric types

- **gauge**: the latest value from each poll is set directly. This fits most Datadog metrics (ratios, rates, timings).
- **counter**: Datadog's API returns an absolute value per poll, not a delta, so counters are derived: the exporter tracks the last value seen per series and `Add()`s the delta on each poll. A negative delta (e.g. from a metric reset) is ignored rather than applied, to avoid corrupting the Prometheus counter.

## Scraping metric groups

Metrics can also be scraped based on a metric label. This can be enabled by providing
grouping configuration.

Once configured, the endpoint for a group scrape looks like `:9091/metrics/$label?target=$value` and returns only metrics with the `$label` set to `$value`.

The `grouping.groupReadyCondition` config section provides options to declare the behaviour
for situations where too few metrics are available yet (e.g. right after startup, before the
first poll of every flow has completed), failing a scrape on the `/metrics/$label` endpoint when
the conditions are not satisfied. This will result in the default metric `up`
on the scraper side to highlight that metrics could not be acquired. Right now, the `minMetrics`
condition is supported, failing a scrape when less than `minMetrics` metrics would be exposed.

The `target` parameter to supply a filter for the label makes this scrape
endpoint compatible with the [`Probe`](https://prometheus-operator.dev/docs/operator/design/#probe)
CRD from the Prometheus operator.

### Example

```yaml
grouping:
- label: probe
  groupReadyCondition:
    minMetrics: 1
```

## Observability
Observability metrics for flow polling and the go runtime are available on observability endpoint `:9090/metrics`.

| Metric name| Metric type | Labels |
| ---------- | ----------- | ------ |
| ddpe_flow_metrics_received_total | Counter | `flow`=&lt;flow name&gt; |
| ddpe_flow_metrics_failed_total | Counter | `flow`=&lt;flow name&gt; |
| ddpe_flow_poll_errors_total | Counter | `flow`=&lt;flow name&gt; |
| ddpe_flow_last_received_seconds | Gauge | `flow`=&lt;flow name&gt; |

`ddpe_flow_poll_errors_total` counts whole-poll failures (HTTP/auth/decode errors against the Datadog API). `ddpe_flow_metrics_failed_total` counts individual series within an otherwise successful poll that couldn't be converted (e.g. no non-null data point in the lookback window, or a template render error).

## Known issues
- when counter metrics are used, running multiple replicas of the exporter will yield wrong results: each replica independently tracks its own last-seen-value state in memory, so each would compute and apply its own delta against the same absolute Datadog value, double (or N-times) counting increments.
- a Datadog query without a `by {tag}` clause returns a single ungrouped series; any label template referencing a tag in that case renders as `<no value>` rather than erroring.
