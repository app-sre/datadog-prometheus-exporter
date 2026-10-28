package serve

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"datadog-prometheus-exporter/config"
	. "datadog-prometheus-exporter/utils"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// dd metrics state
	ddRegistry = prometheus.NewRegistry()

	registryMu sync.Mutex
	ddGauges   = make(map[string]*prometheus.GaugeVec)
	ddCounters = make(map[string]*prometheus.CounterVec)

	// Counters can't be Set() directly: Datadog's sum(...) queries return a
	// per-bucket count, not a cumulative total, so each bucket is an
	// independent increment rather than a delta against the previous value.
	// We track the timestamp (ms) through which a series has already been
	// counted, per series fingerprint, and sum any newer buckets each poll.
	counterMu            sync.Mutex
	lastCounterTimestamp = make(map[string]int64)

	// self observability
	flowMetricsReceived *prometheus.CounterVec
	flowMetricsFailed   *prometheus.CounterVec
	flowPollErrors      *prometheus.CounterVec
	flowLastReceived    *prometheus.GaugeVec
)

func setupObservability(observabilityPort int) {
	flowMetricsReceived = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ddpe_flow_metrics_received_total",
		Help: "Number of Datadog series successfully converted into Prometheus metrics",
	}, []string{"flow"})
	flowMetricsFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ddpe_flow_metrics_failed_total",
		Help: "Number of Datadog series that failed to convert into Prometheus metrics",
	}, []string{"flow"})
	flowPollErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ddpe_flow_poll_errors_total",
		Help: "Number of failed polls against the Datadog API (HTTP, auth, or decode errors)",
	}, []string{"flow"})
	flowLastReceived = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ddpe_flow_last_received_seconds",
		Help: "Timestamp of the last successful poll that produced at least one metric",
	}, []string{"flow"})
	prometheus.MustRegister(flowMetricsReceived)
	prometheus.MustRegister(flowMetricsFailed)
	prometheus.MustRegister(flowPollErrors)
	prometheus.MustRegister(flowLastReceived)

	obsMux := mux.NewRouter()
	obsMux.Handle("/metrics", promhttp.Handler())
	obsServer := &http.Server{Addr: fmt.Sprintf(":%v", observabilityPort), Handler: obsMux}
	go func() {
		if err := obsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Log().Fatalf("observability server failure: %+s", err)
		}
	}()
	Log().Infof("Observability server listening on port %v", observabilityPort)
}

func setupPolling(cfg *config.Config, ctx context.Context) {
	for i := range cfg.Flows {
		fp := cfg.Flows[i]
		// pre-register so the counters show up at 0 before the first poll
		flowMetricsReceived.WithLabelValues(fp.Name)
		flowMetricsFailed.WithLabelValues(fp.Name)
		flowPollErrors.WithLabelValues(fp.Name)
		go pollLoop(ctx, cfg.Dd, fp)
	}
}

func pollLoop(ctx context.Context, dd config.Dd, fp config.FlowProgram) {
	poll(dd, fp)

	ticker := time.NewTicker(fp.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll(dd, fp)
		}
	}
}

func poll(dd config.Dd, fp config.FlowProgram) {
	now := time.Now()
	from := now.Add(-fp.Lookback)

	resp, err := queryDatadog(dd.Site, dd.ApiKey, dd.AppKey, fp.Query, from, now)
	if err != nil {
		flowPollErrors.WithLabelValues(fp.Name).Inc()
		Log().Errorf("flow %s: datadog query failed: %+s", fp.Name, err)
		return
	}

	attrs := resp.Data.Attributes
	received := 0
	for i, s := range attrs.Series {
		if i >= len(attrs.Values) {
			flowMetricsFailed.WithLabelValues(fp.Name).Inc()
			continue
		}

		vars := config.NameTemplateVars{
			DatadogMetricName: fp.Name,
			DatadogTags:       parseTags(s.GroupTags),
		}

		ok, err := applyMetric(fp, vars, attrs.Times, attrs.Values[i])
		if err != nil {
			flowMetricsFailed.WithLabelValues(fp.Name).Inc()
			Log().Errorf("flow %s: failed to apply metric: %+s", fp.Name, err)
			continue
		}
		if !ok {
			// gauge: no settled data point yet in the lookback window.
			// counter: no new bucket since the last poll. Neither is an error.
			continue
		}
		flowMetricsReceived.WithLabelValues(fp.Name).Inc()
		received++
	}

	if received > 0 {
		flowLastReceived.WithLabelValues(fp.Name).SetToCurrentTime()
	}
}

// applyMetric renders the metric's name/labels and records the latest
// value(s). It returns ok=false (with a nil error) when there's simply
// nothing new to record yet - a gauge with no settled point in the lookback
// window, or a counter with no bucket newer than what's already been
// counted - which the caller treats as a quiet, non-error poll outcome.
func applyMetric(fp config.FlowProgram, vars config.NameTemplateVars, times []int64, values []*float64) (bool, error) {
	mt := fp.PrometheusMetricTemplate
	name, labelNames, labelValues, err := buildPrometheusMetadata(mt, vars)
	if err != nil {
		return false, err
	}

	switch mt.Type {
	case "gauge":
		value, ok := lastNonNull(values)
		if !ok {
			return false, nil
		}
		getGauge(name, labelNames).WithLabelValues(labelValues...).Set(value)
		return true, nil
	case "counter":
		fingerprint := strings.Join(append([]string{fp.Name, name}, labelValues...), "|")
		sum, watermark, hasNew := counterIncrement(fingerprint, times, values)
		if !hasNew {
			return false, nil
		}
		counterMu.Lock()
		lastCounterTimestamp[fingerprint] = watermark
		counterMu.Unlock()
		// Always register the series, even at sum=0: a counter that never
		// increments (e.g. zero check failures) must still exist at 0, not
		// be silently absent - downstream consumers (rate()/increase(), and
		// this exporter's own groupReadyCondition gate) depend on the series
		// existing.
		getCounter(name, labelNames).WithLabelValues(labelValues...).Add(sum)
		return true, nil
	default:
		return false, fmt.Errorf("unknown metric type %q", mt.Type)
	}
}

// counterIncrement sums every non-null point newer than the series'
// recorded watermark (the timestamp through which it's already been
// counted). Datadog's sum(...) rollup buckets are independent per-bucket
// counts, not a running total, so correctness here means summing every
// not-yet-seen bucket rather than diffing against a single previous value -
// that stays correct regardless of how the poll interval lines up with the
// query's bucket size (handles both faster and slower polling, and misses).
func counterIncrement(fingerprint string, times []int64, values []*float64) (sum float64, watermark int64, hasNew bool) {
	counterMu.Lock()
	lastTs, seen := lastCounterTimestamp[fingerprint]
	counterMu.Unlock()

	watermark = lastTs
	for i, v := range values {
		if v == nil {
			continue
		}
		if seen && times[i] <= lastTs {
			continue
		}
		sum += *v
		hasNew = true
		if times[i] > watermark {
			watermark = times[i]
		}
	}
	return sum, watermark, hasNew
}

// buildPrometheusMetadata renders a metric's name and label values. Label
// names are sorted so the Vec's label schema (fixed at first registration)
// and every later WithLabelValues call agree on order.
func buildPrometheusMetadata(metric config.PrometheusMetric, vars config.NameTemplateVars) (string, []string, []string, error) {
	name, err := metric.GetMetricName(vars)
	if err != nil {
		return "", nil, nil, err
	}

	labelNames := make([]string, 0, len(metric.Labels))
	for labelName := range metric.Labels {
		labelNames = append(labelNames, labelName)
	}
	sort.Strings(labelNames)

	labelValues := make([]string, len(labelNames))
	for i, labelName := range labelNames {
		value, err := metric.GetLabelValue(labelName, vars)
		if err != nil {
			return "", nil, nil, err
		}
		labelValues[i] = value
	}

	return name, labelNames, labelValues, nil
}

func getGauge(name string, labelNames []string) *prometheus.GaugeVec {
	registryMu.Lock()
	defer registryMu.Unlock()
	g, ok := ddGauges[name]
	if !ok {
		g = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name}, labelNames)
		ddGauges[name] = g
		ddRegistry.MustRegister(g)
	}
	return g
}

func getCounter(name string, labelNames []string) *prometheus.CounterVec {
	registryMu.Lock()
	defer registryMu.Unlock()
	c, ok := ddCounters[name]
	if !ok {
		c = prometheus.NewCounterVec(prometheus.CounterOpts{Name: name}, labelNames)
		ddCounters[name] = c
		ddRegistry.MustRegister(c)
	}
	return c
}

func serve(cfg *config.Config, listenPort int, ctx context.Context) {
	// configure and start scrape server
	mux := mux.NewRouter()
	mux.HandleFunc("/ready", readinessHandler)
	mux.HandleFunc("/healthy", livenessHandler)
	mux.HandleFunc("/metrics", metricsHandler)
	for _, g := range cfg.Groupings {
		mux.HandleFunc(fmt.Sprintf("/metrics/%s", g.Label), func(rw http.ResponseWriter, r *http.Request) {
			probeHandler(g, rw, r)
		})
	}
	server := &http.Server{Addr: fmt.Sprintf(":%v", listenPort), Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Log().Fatalf("metrics server failure: %+s", err)
		}
	}()
	Log().Infof("Scrape server listening on port %v", listenPort)

	<-ctx.Done()

	Log().Info("Server stopped")

	ctxShutDown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer func() {
		cancel()
	}()

	if err := server.Shutdown(ctxShutDown); err != nil {
		Log().Errorf("server Shutdown Failed: %+s", err)
	}
}

func CollectAndServe(configFile string, listenPort int, observabilityPort int, ctx context.Context) {
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		Log().Errorf("failed to load config: %+s", err)
		return
	}
	setupObservability(observabilityPort)
	setupPolling(cfg, ctx)
	serve(cfg, listenPort, ctx)
}

func readinessHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func livenessHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func probeHandler(grouping config.Grouping, w http.ResponseWriter, r *http.Request) {
	// blackbox exporter / Probe CRD compatible scrape handler
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(5*float64(time.Second)))
	defer cancel()
	r = r.WithContext(ctx)

	targetValue, ok := r.URL.Query()["target"]
	if ok && len(targetValue) > 0 {
		metricGatherer := &FilteringRegistry{
			Registry:    ddRegistry,
			Grouping:    grouping,
			FilterValue: targetValue[0],
		}
		h := promhttp.HandlerFor(metricGatherer, promhttp.HandlerOpts{})
		h.ServeHTTP(w, r)
		return
	} else {
		w.WriteHeader(http.StatusBadRequest)
	}
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	// renders all metrics
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(5*float64(time.Second)))
	defer cancel()
	r = r.WithContext(ctx)
	h := promhttp.HandlerFor(ddRegistry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}
