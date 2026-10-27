package config_test

import (
	"datadog-prometheus-exporter/config"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExampleCatchpointChecks(t *testing.T) {
	c, err := config.LoadConfig("../examples/1_catchpoint_checks.yml")
	assert.Nil(t, err)
	assert.Equal(t, "xxx", c.Dd.ApiKey)
	assert.Equal(t, "xxx", c.Dd.AppKey)
	assert.Equal(t, "datadoghq.com", c.Dd.Site)
	assert.Equal(t, 2, len(c.Flows))
	assert.Equal(t, 60*time.Second, c.Flows[0].PollInterval)
	assert.Equal(t, 5*time.Minute, c.Flows[0].Lookback)
	assert.Equal(t, "counter", c.Flows[0].PrometheusMetricTemplate.Type)
	assert.Equal(t, "counter", c.Flows[1].PrometheusMetricTemplate.Type)
}

func TestGetMetricName(t *testing.T) {
	c, err := config.LoadConfig("../examples/1_catchpoint_checks.yml")
	assert.Nil(t, err)
	mt := c.Flows[0].PrometheusMetricTemplate

	name, err := mt.GetMetricName(config.NameTemplateVars{
		DatadogMetricName: "catchpoint-checks",
	})
	assert.Nil(t, err)
	assert.Equal(t, "catchpoint_checks_total", name)
}

func TestGetMetricNameDefaultsToDatadogMetricName(t *testing.T) {
	mt := config.PrometheusMetric{Type: "gauge"}
	assert.Nil(t, mt.Validate())

	name, err := mt.GetMetricName(config.NameTemplateVars{DatadogMetricName: "some_flow"})
	assert.Nil(t, err)
	assert.Equal(t, "some_flow", name)
}

func TestGetLabelValue(t *testing.T) {
	c, err := config.LoadConfig("../examples/1_catchpoint_checks.yml")
	assert.Nil(t, err)
	mt := c.Flows[0].PrometheusMetricTemplate

	// the example config's probe label strips Datadog's leading "_" via trimPrefix
	x, err := mt.GetLabelValue("probe", config.NameTemplateVars{
		DatadogTags: map[string]string{"testname": "_some_probe"},
	})
	assert.Nil(t, err)
	assert.Equal(t, "some_probe", x)
}

func TestTrimPrefixTemplateFunc(t *testing.T) {
	metric := config.PrometheusMetric{
		Type: "gauge",
		Labels: map[string]string{
			"probe":  `{{ trimPrefix "_" .DatadogTags.testname }}`,
			"piped":  `{{ .DatadogTags.testname | trimPrefix "_" }}`,
			"absent": `{{ trimPrefix "_" .DatadogTags.other }}`,
		},
	}
	assert.Nil(t, metric.Validate())

	vars := config.NameTemplateVars{DatadogTags: map[string]string{
		"testname": "_app-sre-test-quayio-ue2-pull-monitor-v2",
		"other":    "no-leading-underscore",
	}}

	v, err := metric.GetLabelValue("probe", vars)
	assert.Nil(t, err)
	assert.Equal(t, "app-sre-test-quayio-ue2-pull-monitor-v2", v)

	v, err = metric.GetLabelValue("piped", vars)
	assert.Nil(t, err)
	assert.Equal(t, "app-sre-test-quayio-ue2-pull-monitor-v2", v)

	// strings.TrimPrefix is a no-op when the prefix isn't present
	v, err = metric.GetLabelValue("absent", vars)
	assert.Nil(t, err)
	assert.Equal(t, "no-leading-underscore", v)
}

func TestMinMetricsNotAUInt(t *testing.T) {
	configFile := `---
dd:
  apiKey: xxx
  appKey: xxx
flows:
- name: catchpoint-data
  query: "avg:catchpoint.success.rate{*} by {testname}"
  prometheusMetricTemplate:
    type: gauge
    labels:
      probe: '{{ .DatadogTags.testname }}'
grouping:
- label: probe
  groupReadyCondition:
    minMetrics: -1
`
	_, err := config.LoadConfigFromBytes([]byte(configFile))
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "`-1` into uint")
}

func TestDefaultPollIntervalAndLookback(t *testing.T) {
	configFile := `---
dd:
  apiKey: xxx
  appKey: xxx
flows:
- name: catchpoint-data
  query: "avg:catchpoint.success.rate{*} by {testname}"
  prometheusMetricTemplate:
    type: gauge
    labels:
      probe: '{{ .DatadogTags.testname }}'
`
	cfg, err := config.LoadConfigFromBytes([]byte(configFile))
	assert.Nil(t, err)
	assert.NotNil(t, cfg)

	assert.Equal(t, 60*time.Second, cfg.Flows[0].PollInterval)
	assert.Equal(t, 5*time.Minute, cfg.Flows[0].Lookback)
}

func TestCustomPollIntervalAndLookback(t *testing.T) {
	configFile := `---
dd:
  apiKey: xxx
  appKey: xxx
flows:
- name: catchpoint-data
  query: "avg:catchpoint.success.rate{*} by {testname}"
  pollInterval: 30s
  lookback: 10m
  prometheusMetricTemplate:
    type: gauge
    labels:
      probe: '{{ .DatadogTags.testname }}'
`
	cfg, err := config.LoadConfigFromBytes([]byte(configFile))
	assert.Nil(t, err)

	thirty, _ := time.ParseDuration("30s")
	ten_min, _ := time.ParseDuration("10m")
	assert.Equal(t, thirty, cfg.Flows[0].PollInterval)
	assert.Equal(t, ten_min, cfg.Flows[0].Lookback)
}

func TestMissingMetricTypeIsRejected(t *testing.T) {
	configFile := `---
dd:
  apiKey: xxx
  appKey: xxx
flows:
- name: catchpoint-data
  query: "avg:catchpoint.success.rate{*} by {testname}"
  prometheusMetricTemplate:
    labels:
      probe: '{{ .DatadogTags.testname }}'
`
	_, err := config.LoadConfigFromBytes([]byte(configFile))
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "prometheusMetricTemplate.type")
}

func TestMissingCredentialsAreRejected(t *testing.T) {
	configFile := `---
dd:
  apiKey: xxx
flows:
- name: catchpoint-data
  query: "avg:catchpoint.success.rate{*} by {testname}"
  prometheusMetricTemplate:
    type: gauge
    labels:
      probe: '{{ .DatadogTags.testname }}'
`
	_, err := config.LoadConfigFromBytes([]byte(configFile))
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "appKey")
}
