package data

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	view "go-wind-admin/api/gen/go/inference/service/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type InferenceMetricsClient struct {
	address, namespace string
	client             *http.Client
}

func NewInferenceMetricsClient(address, namespace string) (*InferenceMetricsClient, error) {
	if address == "" {
		return nil, nil
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || namespace == "" {
		return nil, fmt.Errorf("invalid managed metrics configuration")
	}
	return &InferenceMetricsClient{strings.TrimRight(address, "/"), namespace, &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *InferenceMetricsClient) Query(ctx context.Context, in *view.InferenceMetricsRequest, instances []*view.RuntimeInstance) (*view.InferenceMetricsResponse, error) {
	out := &view.InferenceMetricsResponse{Start: in.Start, End: in.End, Step: in.Step, Series: []*view.InferenceMetricSeries{}}
	ids := make([]string, 0, len(instances)*2)
	for _, pod := range instances {
		if pod == nil {
			return nil, fmt.Errorf("nil runtime instance")
		}
		id, err := uuid.Parse(pod.Uid)
		if err != nil {
			return nil, err
		}
		ids = append(ids, regexp.QuoteMeta(id.String()), regexp.QuoteMeta(strings.ReplaceAll(id.String(), "-", "_")))
	}
	if len(ids) == 0 {
		return out, nil
	}
	// cAdvisor's id contains the immutable Kubernetes Pod UID in either cgroup
	// layout. Namespace + UID excludes prior/replacement Pods with the same name.
	selector := fmt.Sprintf(`namespace=%s,container!="",container!="POD",id=~%s`, strconv.Quote(c.namespace), strconv.Quote(".*pod("+strings.Join(ids, "|")+").*"))
	queries := []struct{ name, unit, query string }{
		{"cpu", "cores", "sum(rate(container_cpu_usage_seconds_total{" + selector + "}[2m]))"},
		{"memory", "bytes", "sum(container_memory_working_set_bytes{" + selector + "})"},
	}
	for _, q := range queries {
		params := url.Values{"query": {q.query}, "start": {strconv.FormatFloat(float64(in.Start.AsTime().UnixNano())/1e9, 'f', 3, 64)}, "end": {strconv.FormatFloat(float64(in.End.AsTime().UnixNano())/1e9, 'f', 3, 64)}, "step": {strconv.Itoa(int(in.Step))}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.address+"/api/v1/query_range?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		var result struct {
			Status string `json:"status"`
			Data   struct {
				ResultType string `json:"resultType"`
				Result     []struct {
					Values [][]json.RawMessage `json:"values"`
				} `json:"result"`
			} `json:"data"`
			Warnings []string `json:"warnings"`
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || len(body) > 2*1024*1024 || json.Unmarshal(body, &result) != nil || result.Status != "success" || result.Data.ResultType != "matrix" || len(result.Warnings) > 0 || len(result.Data.Result) > 1 {
			return nil, fmt.Errorf("invalid metrics source response")
		}
		series := &view.InferenceMetricSeries{Name: q.name, Unit: q.unit, Points: []*view.InferenceMetricPoint{}}
		for _, row := range result.Data.Result {
			for _, sample := range row.Values {
				if len(sample) != 2 {
					return nil, fmt.Errorf("invalid metric sample")
				}
				var timestamp float64
				var raw string
				if json.Unmarshal(sample[0], &timestamp) != nil || json.Unmarshal(sample[1], &raw) != nil {
					return nil, fmt.Errorf("invalid metric sample")
				}
				value, err := strconv.ParseFloat(raw, 64)
				if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
					continue
				}
				at := time.Unix(0, int64(timestamp*1e9))
				if at.Before(in.Start.AsTime().Add(-time.Millisecond)) || at.After(in.End.AsTime().Add(time.Millisecond)) {
					return nil, fmt.Errorf("metric sample outside requested range")
				}
				series.Points = append(series.Points, &view.InferenceMetricPoint{Timestamp: timestamppb.New(at), Value: value})
			}
		}
		out.Series = append(out.Series, series)
	}
	return out, nil
}
