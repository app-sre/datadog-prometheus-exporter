package serve

import (
	"fmt"

	"datadog-prometheus-exporter/config"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type FilteringRegistry struct {
	Registry    prometheus.Gatherer
	Grouping    config.Grouping
	FilterValue string
}

func (fr *FilteringRegistry) Gather() ([]*dto.MetricFamily, error) {
	var metricCount uint = 0
	mfs, err := fr.Registry.Gather()
	if err != nil {
		return nil, err
	}

	filteredMfs := []*dto.MetricFamily{}
	for _, mf := range mfs {
		metrics := []*dto.Metric{}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if *l.Name == fr.Grouping.Label && *l.Value == fr.FilterValue {
					metrics = append(metrics, m)
					metricCount++
					break
				}
			}
		}
		if len(metrics) > 0 {
			filteredMfs = append(filteredMfs, &dto.MetricFamily{
				Name:   mf.Name,
				Help:   mf.Help,
				Type:   mf.Type,
				Metric: metrics,
			})
		}
	}

	if metricCount >= fr.Grouping.GroupReadyCondition.MinMetrics {
		return filteredMfs, nil
	} else {
		return nil, fmt.Errorf("not enough metrics in group. minMetrics = %d", fr.Grouping.GroupReadyCondition.MinMetrics)
	}
}
