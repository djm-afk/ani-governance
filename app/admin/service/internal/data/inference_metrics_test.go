package data

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	view "go-wind-admin/api/gen/go/inference/service/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestInferenceMetricsScopesImmutablePodUIDs(t *testing.T) {
	end := time.Now().UTC().Truncate(time.Second)
	count := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		q := r.URL.Query()
		require.Equal(t, "/api/v1/query_range", r.URL.Path)
		require.Contains(t, q.Get("query"), `namespace="runtime"`)
		require.Contains(t, q.Get("query"), "11111111-1111-4111-8111-111111111111")
		require.Contains(t, q.Get("query"), "11111111_1111_4111_8111_111111111111")
		require.Equal(t, "60", q.Get("step"))
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"values":[[%d,"2.5"]]}]}}`, end.Unix())
	}))
	defer backend.Close()
	client, err := NewInferenceMetricsClient(backend.URL, "runtime")
	require.NoError(t, err)
	req := &view.InferenceMetricsRequest{Start: timestamppb.New(end.Add(-time.Hour)), End: timestamppb.New(end), Step: 60}
	pods := []*view.RuntimeInstance{{Uid: "11111111-1111-4111-8111-111111111111", Name: "untrusted-name"}}
	out, err := client.Query(context.Background(), req, pods)
	require.NoError(t, err)
	require.Len(t, out.Series, 2)
	require.Equal(t, 2.5, out.Series[0].Points[0].Value)
	require.Equal(t, 2, count)
	empty, err := client.Query(context.Background(), req, nil)
	require.NoError(t, err)
	require.Empty(t, empty.Series)
	require.Equal(t, 2, count)
	_, err = client.Query(context.Background(), req, []*view.RuntimeInstance{{Uid: `.*"} or vector(1)`}})
	require.Error(t, err)
	require.Equal(t, 2, count)
}
func TestInferenceMetricsRejectsUnavailableOrPartialSource(t *testing.T) {
	for _, body := range []string{`{"status":"error"}`, `{"status":"success","warnings":["partial"],"data":{"resultType":"matrix"}}`, `{"status":"success","data":{"resultType":"vector"}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer server.Close()
			client, err := NewInferenceMetricsClient(server.URL, "runtime")
			require.NoError(t, err)
			now := time.Now()
			_, err = client.Query(context.Background(), &view.InferenceMetricsRequest{Start: timestamppb.New(now.Add(-time.Hour)), End: timestamppb.New(now), Step: 60}, []*view.RuntimeInstance{{Uid: "11111111-1111-4111-8111-111111111111"}})
			require.Error(t, err)
		})
	}
}

// Opt-in read-only integration against the real Prometheus endpoint. The UID
// belongs to the explicitly selected current Pod; no fake samples are injected.
func TestInferenceMetricsLivePrometheus(t *testing.T) {
	address, uid := os.Getenv("ANI_METRICS_TEST_URL"), os.Getenv("ANI_METRICS_TEST_POD_UID")
	if address == "" || uid == "" {
		t.Skip("live metrics source not selected")
	}
	client, err := NewInferenceMetricsClient(address, os.Getenv("ANI_METRICS_TEST_NAMESPACE"))
	require.NoError(t, err)
	now := time.Now().UTC()
	out, err := client.Query(context.Background(), &view.InferenceMetricsRequest{Start: timestamppb.New(now.Add(-10 * time.Minute)), End: timestamppb.New(now), Step: 60}, []*view.RuntimeInstance{{Uid: uid}})
	require.NoError(t, err)
	require.Len(t, out.Series, 2)
	for _, series := range out.Series {
		require.NotEmpty(t, series.Points, series.Name)
		require.False(t, strings.Contains(series.Name, "simulated"))
		t.Log(series.Name, "real samples", len(series.Points))
	}
}
