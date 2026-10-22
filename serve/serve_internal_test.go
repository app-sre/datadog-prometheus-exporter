package serve

import (
	"testing"

	"datadog-prometheus-exporter/config"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func f(v float64) *float64 { return &v }

func TestLastNonNull(t *testing.T) {
	v, ok := lastNonNull([]*float64{f(1), f(2), nil})
	assert.True(t, ok)
	assert.Equal(t, 2.0, v)

	v, ok = lastNonNull([]*float64{nil, nil})
	assert.False(t, ok)
	assert.Equal(t, 0.0, v)

	v, ok = lastNonNull([]*float64{})
	assert.False(t, ok)
	assert.Equal(t, 0.0, v)
}

func TestParseTags(t *testing.T) {
	tags := parseTags([]string{"testname:_app-sre-test-quayio-ue2-pull-monitor-v2", "errortype:success", "noValuePart"})
	assert.Equal(t, "_app-sre-test-quayio-ue2-pull-monitor-v2", tags["testname"])
	assert.Equal(t, "success", tags["errortype"])
	assert.Equal(t, "", tags["noValuePart"])
}

func TestBuildPrometheusMetadataIsOrderStable(t *testing.T) {
	metric := config.PrometheusMetric{
		Name: "catchpoint_success_rate",
		Type: "gauge",
		Labels: map[string]string{
			"probe": "{{ .DatadogTags.testname }}",
			"zzz":   "static",
		},
	}
	assert.Nil(t, metric.Validate())

	vars := config.NameTemplateVars{
		DatadogMetricName: "catchpoint-success-rate",
		DatadogTags:       map[string]string{"testname": "_some_probe"},
	}

	name, labelNames1, labelValues1, err := buildPrometheusMetadata(metric, vars)
	assert.Nil(t, err)
	assert.Equal(t, "catchpoint_success_rate", name)

	// calling it repeatedly must yield the same label order every time,
	// since the Vec's schema is fixed at first registration
	for i := 0; i < 20; i++ {
		_, labelNames2, labelValues2, err := buildPrometheusMetadata(metric, vars)
		assert.Nil(t, err)
		assert.Equal(t, labelNames1, labelNames2)
		assert.Equal(t, labelValues1, labelValues2)
	}
}

func TestApplyMetricGauge(t *testing.T) {
	fp := config.FlowProgram{
		Name: "test-flow",
		PrometheusMetricTemplate: config.PrometheusMetric{
			Name: "test_gauge",
			Type: "gauge",
			Labels: map[string]string{
				"probe": "{{ .DatadogTags.testname }}",
			},
		},
	}
	assert.Nil(t, fp.PrometheusMetricTemplate.Validate())

	vars := config.NameTemplateVars{DatadogTags: map[string]string{"testname": "probe-a"}}
	err := applyMetric(fp, vars, 1.0)
	assert.Nil(t, err)

	g := getGauge("test_gauge", []string{"probe"})
	assert.Equal(t, 1.0, testutil.ToFloat64(g.WithLabelValues("probe-a")))
}
