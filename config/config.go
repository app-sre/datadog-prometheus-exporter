package config

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// templateFuncs are available to every PrometheusMetric Name/Labels template.
var templateFuncs = template.FuncMap{
	// trimPrefix uses the common Sprig argument order (prefix, s) so it reads
	// naturally both as `trimPrefix "_" .X` and piped as `{{ .X | trimPrefix "_" }}`.
	// Useful to normalize Datadog tag values that carry a vendor-added prefix
	// (e.g. some Catchpoint tag values get a leading "_") back to what an
	// existing consumer (an alert, a dashboard) expects.
	"trimPrefix": func(prefix, s string) string { return strings.TrimPrefix(s, prefix) },
}

type GroupReadyCondition struct {
	MinMetrics uint `yaml:"minMetrics"`
}

type Grouping struct {
	Label               string
	GroupReadyCondition GroupReadyCondition `yaml:"groupReadyCondition"`
}

// PrometheusMetric describes how a single Datadog query's grouped series are
// translated into one Prometheus metric family.
type PrometheusMetric struct {
	Name           string            `yaml:"name"`
	Type           string            `yaml:"type"`
	Labels         map[string]string `yaml:"labels"`
	nameTemplate   template.Template
	labelTemplates map[string]template.Template
}

// NameTemplateVars is the data available to the go-template fields of a
// PrometheusMetric (Name/Labels). DatadogTags is built by splitting each
// `key:value` entry of a Datadog series' group_tags on the first colon.
type NameTemplateVars struct {
	DatadogMetricName string
	DatadogTags       map[string]string
}

func (pm *PrometheusMetric) Validate() error {
	// name template
	name := pm.Name
	if name == "" {
		name = "{{ .DatadogMetricName }}"
	}
	tmpl, err := template.New("x").Funcs(templateFuncs).Parse(name)
	if err != nil {
		return err
	}
	pm.nameTemplate = *tmpl

	// label templates
	labelTemplates := map[string]template.Template{}
	for labelName, labelValue := range pm.Labels {
		tmpl, err := template.New("x").Funcs(templateFuncs).Parse(labelValue)
		if err != nil {
			return err
		}
		labelTemplates[labelName] = *tmpl
	}
	pm.labelTemplates = labelTemplates

	return nil
}

func (pm *PrometheusMetric) GetMetricName(data NameTemplateVars) (string, error) {
	var buffer bytes.Buffer
	err := pm.nameTemplate.Execute(&buffer, data)
	return buffer.String(), err
}

func (pm *PrometheusMetric) GetLabelValue(labelName string, data NameTemplateVars) (string, error) {
	tmpl, ok := pm.labelTemplates[labelName]
	if !ok {
		return "", fmt.Errorf("could not find label named %s", labelName)
	}
	var buffer bytes.Buffer
	err := tmpl.Execute(&buffer, data)
	return buffer.String(), err
}

// FlowProgram describes one Datadog metrics query to poll on an interval.
//
// Unlike the SignalFx exporter's FlowProgram, there is no "stream" concept:
// a single Datadog query+formula always yields one homogeneous set of
// grouped series, so there is exactly one PrometheusMetricTemplate per flow.
type FlowProgram struct {
	Name                     string           `yaml:"name"`
	Query                    string           `yaml:"query"`
	PollInterval             time.Duration    `yaml:"pollInterval"`
	Lookback                 time.Duration    `yaml:"lookback"`
	PrometheusMetricTemplate PrometheusMetric `yaml:"prometheusMetricTemplate"`
}

const (
	defaultPollInterval = 60 * time.Second
	// Lookback must comfortably exceed both the poll interval and Datadog's
	// own ingestion/rollup delay, so a single missed poll or a slow-to-land
	// data point doesn't create a gap.
	defaultLookback = 5 * time.Minute
)

func (fp *FlowProgram) Validate() error {
	if fp.Name == "" {
		return fmt.Errorf("flow: name must not be empty")
	}
	if fp.Query == "" {
		return fmt.Errorf("flow %s: query must not be empty", fp.Name)
	}
	if fp.PollInterval == 0 {
		fp.PollInterval = defaultPollInterval
	}
	if fp.Lookback == 0 {
		fp.Lookback = defaultLookback
	}
	if fp.PrometheusMetricTemplate.Type != "gauge" && fp.PrometheusMetricTemplate.Type != "counter" {
		return fmt.Errorf("flow %s: prometheusMetricTemplate.type must be 'gauge' or 'counter', got %q", fp.Name, fp.PrometheusMetricTemplate.Type)
	}
	return fp.PrometheusMetricTemplate.Validate()
}

// Dd holds Datadog API credentials and the site to query.
type Dd struct {
	Site   string `yaml:"site"`
	ApiKey string `yaml:"apiKey"`
	AppKey string `yaml:"appKey"`
}

func (dd *Dd) Validate() error {
	if dd.Site == "" {
		dd.Site = "datadoghq.com"
	}
	if dd.ApiKey == "" {
		return fmt.Errorf("dd: apiKey must not be empty")
	}
	if dd.AppKey == "" {
		return fmt.Errorf("dd: appKey must not be empty")
	}
	return nil
}

type Config struct {
	Dd        Dd            `yaml:"dd"`
	Flows     []FlowProgram `yaml:"flows"`
	Groupings []Grouping    `yaml:"grouping"`
}

func (c *Config) Validate() error {
	if err := c.Dd.Validate(); err != nil {
		return err
	}
	for i := range c.Flows {
		fp := &c.Flows[i]
		if err := fp.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func LoadConfigFromBytes(configBytes []byte) (*Config, error) {
	var cfg Config
	err := yaml.Unmarshal(configBytes, &cfg)
	if err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LoadConfig(file string) (*Config, error) {
	configBytes, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return LoadConfigFromBytes(configBytes)
}
