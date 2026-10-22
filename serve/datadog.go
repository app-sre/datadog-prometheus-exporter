package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

type ddFormula struct {
	Formula string `json:"formula"`
}

type ddQueryDef struct {
	Name       string `json:"name"`
	DataSource string `json:"data_source"`
	Query      string `json:"query"`
}

type ddTimeseriesRequestAttrs struct {
	Formulas []ddFormula  `json:"formulas"`
	Queries  []ddQueryDef `json:"queries"`
	From     int64        `json:"from"`
	To       int64        `json:"to"`
}

type ddTimeseriesRequestData struct {
	Type       string                   `json:"type"`
	Attributes ddTimeseriesRequestAttrs `json:"attributes"`
}

type ddTimeseriesRequest struct {
	Data ddTimeseriesRequestData `json:"data"`
}

type ddSeries struct {
	GroupTags []string `json:"group_tags"`
}

type ddTimeseriesAttributes struct {
	Series []ddSeries   `json:"series"`
	Times  []int64      `json:"times"`
	Values [][]*float64 `json:"values"`
}

type ddTimeseriesResponseData struct {
	Attributes ddTimeseriesAttributes `json:"attributes"`
}

type ddTimeseriesResponse struct {
	Data   ddTimeseriesResponseData `json:"data"`
	Errors []string                 `json:"errors"`
}

// queryDatadog issues a single POST /api/v2/query/timeseries call for the
// given Datadog query over [from, to]. The response is columnar: Data.Attributes.Series[i]
// and Data.Attributes.Values[i] correspond by index, and each Values[i] entry
// is aligned with Data.Attributes.Times by index. Values can be nil (JSON null)
// for incomplete/missing buckets, most commonly the most recent one.
func queryDatadog(site, apiKey, appKey, query string, from, to time.Time) (*ddTimeseriesResponse, error) {
	reqBody := ddTimeseriesRequest{
		Data: ddTimeseriesRequestData{
			Type: "timeseries_request",
			Attributes: ddTimeseriesRequestAttrs{
				Formulas: []ddFormula{{Formula: "a"}},
				Queries:  []ddQueryDef{{Name: "a", DataSource: "metrics", Query: query}},
				From:     from.UnixMilli(),
				To:       to.UnixMilli(),
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("https://api.%s/api/v2/query/timeseries", site)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DD-API-KEY", apiKey)
	req.Header.Set("DD-APPLICATION-KEY", appKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("datadog API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var tsResp ddTimeseriesResponse
	if err := json.Unmarshal(respBody, &tsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if len(tsResp.Errors) > 0 {
		return nil, fmt.Errorf("datadog API returned errors: %s", strings.Join(tsResp.Errors, "; "))
	}

	return &tsResp, nil
}

// lastNonNull returns the last non-null value in a Values[i] row, walking
// backwards so a still-settling most-recent bucket doesn't shadow the latest
// real data point.
func lastNonNull(values []*float64) (float64, bool) {
	for i := len(values) - 1; i >= 0; i-- {
		if values[i] != nil {
			return *values[i], true
		}
	}
	return 0, false
}

// parseTags splits group_tags entries ("key:value") into a map. Datadog tag
// values are used verbatim, including any leading underscore Datadog itself
// applies to some tag values (e.g. Catchpoint testnames).
func parseTags(groupTags []string) map[string]string {
	tags := make(map[string]string, len(groupTags))
	for _, gt := range groupTags {
		parts := strings.SplitN(gt, ":", 2)
		if len(parts) == 2 {
			tags[parts[0]] = parts[1]
		} else {
			tags[parts[0]] = ""
		}
	}
	return tags
}
