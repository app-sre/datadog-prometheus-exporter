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
	ok, err := applyMetric(fp, vars, []int64{1000}, []*float64{f(1.0)})
	assert.Nil(t, err)
	assert.True(t, ok)

	g := getGauge("test_gauge", []string{"probe"})
	assert.Equal(t, 1.0, testutil.ToFloat64(g.WithLabelValues("probe-a")))
}

func TestApplyMetricGaugeNoSettledPoint(t *testing.T) {
	fp := config.FlowProgram{
		Name: "test-flow",
		PrometheusMetricTemplate: config.PrometheusMetric{
			Name: "test_gauge_unsettled",
			Type: "gauge",
		},
	}
	assert.Nil(t, fp.PrometheusMetricTemplate.Validate())

	ok, err := applyMetric(fp, config.NameTemplateVars{}, []int64{1000}, []*float64{nil})
	assert.Nil(t, err)
	assert.False(t, ok)
}

func TestApplyMetricUnknownType(t *testing.T) {
	fp := config.FlowProgram{
		Name: "test-flow",
		PrometheusMetricTemplate: config.PrometheusMetric{
			Name: "test_unknown",
			Type: "histogram",
		},
	}
	assert.Nil(t, fp.PrometheusMetricTemplate.Validate())

	ok, err := applyMetric(fp, config.NameTemplateVars{}, []int64{1000}, []*float64{f(1.0)})
	assert.NotNil(t, err)
	assert.False(t, ok)
}

func TestApplyMetricCounterSumsNewBucketsOnly(t *testing.T) {
	fp := config.FlowProgram{
		Name: "test-flow-counter",
		PrometheusMetricTemplate: config.PrometheusMetric{
			Name: "test_counter_total",
			Type: "counter",
			Labels: map[string]string{
				"probe": "{{ .DatadogTags.testname }}",
			},
		},
	}
	assert.Nil(t, fp.PrometheusMetricTemplate.Validate())
	vars := config.NameTemplateVars{DatadogTags: map[string]string{"testname": "probe-counter"}}

	// first poll: no prior watermark, every bucket in the window is new
	ok, err := applyMetric(fp, vars, []int64{1000, 2000, 3000}, []*float64{f(180), f(180), f(180)})
	assert.Nil(t, err)
	assert.True(t, ok)
	c := getCounter("test_counter_total", []string{"probe"})
	assert.Equal(t, 540.0, testutil.ToFloat64(c.WithLabelValues("probe-counter")))

	// second poll overlaps two already-counted buckets and adds one new one:
	// only the new bucket (4000) should be added, not a re-sum of the window
	ok, err = applyMetric(fp, vars, []int64{2000, 3000, 4000}, []*float64{f(180), f(180), f(90)})
	assert.Nil(t, err)
	assert.True(t, ok)
	assert.Equal(t, 630.0, testutil.ToFloat64(c.WithLabelValues("probe-counter")))

	// third poll: no bucket newer than the last watermark -> quiet, not an error
	ok, err = applyMetric(fp, vars, []int64{3000, 4000}, []*float64{f(180), f(90)})
	assert.Nil(t, err)
	assert.False(t, ok)
	assert.Equal(t, 630.0, testutil.ToFloat64(c.WithLabelValues("probe-counter")))
}

func TestApplyMetricCounterSkipsNullBuckets(t *testing.T) {
	fp := config.FlowProgram{
		Name: "test-flow-counter-null",
		PrometheusMetricTemplate: config.PrometheusMetric{
			Name: "test_counter_null_total",
			Type: "counter",
		},
	}
	assert.Nil(t, fp.PrometheusMetricTemplate.Validate())

	// the most recent bucket is still settling (null); it must not be
	// counted now, nor permanently skipped once it does settle
	ok, err := applyMetric(fp, config.NameTemplateVars{}, []int64{1000, 2000}, []*float64{f(10), nil})
	assert.Nil(t, err)
	assert.True(t, ok)
	c := getCounter("test_counter_null_total", []string{})
	assert.Equal(t, 10.0, testutil.ToFloat64(c.WithLabelValues()))

	// next poll: bucket 2000 has now settled and a new bucket 3000 appeared
	ok, err = applyMetric(fp, config.NameTemplateVars{}, []int64{1000, 2000, 3000}, []*float64{f(10), f(20), f(30)})
	assert.Nil(t, err)
	assert.True(t, ok)
	assert.Equal(t, 60.0, testutil.ToFloat64(c.WithLabelValues()))
}
