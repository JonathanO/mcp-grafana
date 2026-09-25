//go:build unit

package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLokiClient_FetchData_PassesMatcherAsQueryParam(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","data":["service"]}`))
	}))
	defer server.Close()

	c := &Client{httpClient: server.Client(), baseURL: server.URL}

	result, err := c.fetchData(context.Background(), "/loki/api/v1/label/service/values", `{namespace="prod"}`, "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"service"}, result)
	assert.Equal(t, `{namespace="prod"}`, gotQuery)
}

func TestLokiClient_FetchData_OmitsQueryParamWhenMatcherEmpty(t *testing.T) {
	var sawQuery bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawQuery = r.URL.Query()["query"]
		_, _ = w.Write([]byte(`{"status":"success","data":["app","pod"]}`))
	}))
	defer server.Close()

	c := &Client{httpClient: server.Client(), baseURL: server.URL}

	result, err := c.fetchData(context.Background(), "/loki/api/v1/labels", "", "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"app", "pod"}, result)
	assert.False(t, sawQuery, "query param should be omitted when matcher is empty")
}

func TestQueryLokiLogsSortsBeforeTruncatingAndPreservesNanosecondBounds(t *testing.T) {
	start, end := time.Unix(1, 1), time.Unix(1, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/loki":
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case "/api/datasources/proxy/uid/loki/loki/api/v1/query_range":
			assert.Equal(t, "3", r.URL.Query().Get("limit"))
			assert.Equal(t, strconv.FormatInt(start.UnixNano(), 10), r.URL.Query().Get("start"))
			assert.Equal(t, strconv.FormatInt(end.UnixNano(), 10), r.URL.Query().Get("end"))
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"streams","result":[
				{"stream":{"app":"a"},"values":[["1000000002","a2"],["1000000001","a1"]]},
				{"stream":{"app":"b"},"values":[["1000000003","b3"]]}
			]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx := enforceTestCtx(server, false)

	for _, tc := range []struct {
		direction string
		lines     []string
	}{
		{direction: "backward", lines: []string{"b3", "a2"}},
		{direction: "forward", lines: []string{"a1", "a2"}},
	} {
		t.Run(tc.direction, func(t *testing.T) {
			result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
				DatasourceUID: "loki", LogQL: `{app=~".+"}`, Limit: 2, Direction: tc.direction,
				StartRFC3339: start.UTC().Format(time.RFC3339Nano), EndRFC3339: end.UTC().Format(time.RFC3339Nano),
			})
			require.NoError(t, err)
			require.Len(t, result.Data, 2)
			assert.Equal(t, tc.lines, []string{result.Data[0].Line, result.Data[1].Line})
			require.NotNil(t, result.Metadata)
			assert.True(t, result.Metadata.ResultsTruncated)
			assert.Equal(t, 2, result.Metadata.LinesReturned)
		})
	}
}
