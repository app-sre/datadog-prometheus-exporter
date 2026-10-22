package serve_test

import (
	"datadog-prometheus-exporter/config"
	"datadog-prometheus-exporter/serve"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

var (
	FilterLabel = "label"
	FilterValue = "value"
)

func setupRegistry(minMetrics uint) (*serve.FilteringRegistry, *prometheus.Registry) {
	registry := prometheus.NewRegistry()

	grouping := config.Grouping{
		Label: FilterLabel,
		GroupReadyCondition: config.GroupReadyCondition{
			MinMetrics: minMetrics,
		},
	}

	fr := &serve.FilteringRegistry{
		Grouping:    grouping,
		Registry:    registry,
		FilterValue: FilterValue,
	}

	return fr, registry
}

func TestFilterGroup(t *testing.T) {
	/*  test only values are return that match the filter */
	fr, registry := setupRegistry(0)

	gauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "some_gauge"},
		[]string{FilterLabel},
	)
	registry.MustRegister(gauge)
	gauge.WithLabelValues(FilterValue).Set(10)
	gauge.WithLabelValues("other_value").Set(11)

	metricFamilies, err := fr.Gather()
	assert.Nil(t, err)
	assert.NotNil(t, metricFamilies)
	assert.NotEmpty(t, metricFamilies)

	metricCounter := 0
	for _, mf := range metricFamilies {
		for _, m := range mf.Metric {
			label := m.GetLabel()[0]
			assert.Equal(t, *label.Name, FilterLabel)
			assert.Equal(t, *label.Value, FilterValue)
			metricCounter++
		}
	}
	assert.Equal(t, metricCounter, 1)
}

func TestMinimumMetricsSupplied(t *testing.T) {
	/* test that the filtering registry is not complaining
	   when the minimum required metrics remain after filtering */

	var minMetrics uint = 2
	fr, registry := setupRegistry(minMetrics)

	gaugeWithMatchingLabel := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "some_gauge"},
		[]string{FilterLabel},
	)
	registry.MustRegister(gaugeWithMatchingLabel)
	counterWithMatchingLabel := prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "some_counter"},
		[]string{FilterLabel},
	)
	registry.MustRegister(counterWithMatchingLabel)

	gaugeWithMatchingLabel.WithLabelValues(FilterValue).Set(0)
	counterWithMatchingLabel.WithLabelValues(FilterValue).Add(1)

	metricFamilies, err := fr.Gather()
	assert.Nil(t, err)
	assert.NotNil(t, metricFamilies)
	assert.NotEmpty(t, metricFamilies)

	var metricCounter uint = 0
	for _, mf := range metricFamilies {
		metricCounter += uint(len(mf.Metric))
	}
	assert.GreaterOrEqual(t, metricCounter, minMetrics)
}

func TestTooFewSupplied(t *testing.T) {
	/* test that the filtering registry raises and error
	   when less metrics remain as demanded after filtering */

	var minMetrics uint = 2
	fr, registry := setupRegistry(minMetrics)

	gaugeWithMatchingLabel := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "some_gauge"},
		[]string{FilterLabel},
	)
	registry.MustRegister(gaugeWithMatchingLabel)
	counterWithMatchingLabel := prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "some_counter"},
		[]string{FilterLabel},
	)
	registry.MustRegister(counterWithMatchingLabel)

	gaugeWithMatchingLabel.WithLabelValues(FilterValue).Set(0)
	counterWithMatchingLabel.WithLabelValues("other_value").Add(1)

	_, err := fr.Gather()
	assert.Error(t, err)
}
