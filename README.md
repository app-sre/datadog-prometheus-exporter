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
- name: catchpoint-checks
  query: "sum:catchpoint.success.rate{*} by {testname}"
  pollInterval: 60s
  lookback: 5m
  prometheusMetricTemplate:
    name: catchpoint_checks_total
    type: counter
    labels:
      probe: '{{ .DatadogTags.testname }}'
```

**Watch out for `avg` vs `sum`:** many Datadog metrics (check the metric's `type` via `GET /api/v1/metrics/<name>`) are Datadog-type `count`, submitting a fixed value per event regardless of outcome. `avg:` over such a metric is always the same constant and carries no signal; `sum:` over the same metric yields a real per-window count. `catchpoint.success.rate` is a real-world example of this despite its name: it's Datadog-type `count`, submits exactly `1` per check execution, and `avg:catchpoint.success.rate{...}` is always `1` — `sum:` is what actually reflects check volume.

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

- **gauge**: the latest non-null value from each poll's lookback window is set directly. This fits most Datadog metrics (ratios, rates, timings).
- **counter**: a `sum(...)`-aggregated Datadog query returns an independent count per rollup bucket, not a cumulative total, so each new bucket is an increment rather than a delta against the previous value. The exporter tracks, per series, the timestamp through which buckets have already been counted, and on each poll sums every bucket newer than that watermark before `Add()`-ing the total. This stays correct regardless of how the poll interval lines up with the query's bucket size — a slow poll just sums more buckets at once; a fast poll correctly finds nothing new yet.

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
- when counter metrics are used, running multiple replicas of the exporter will yield wrong results: each replica independently tracks its own per-series watermark in memory, so each would sum and apply the same Datadog buckets, double (or N-times) counting increments.
- a Datadog query without a `by {tag}` clause returns a single ungrouped series; any label template referencing a tag in that case renders as `<no value>` rather than erroring.
