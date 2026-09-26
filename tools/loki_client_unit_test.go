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

// Loki timestamps are nanosecond-resolution, so a bound one nanosecond off a
// second boundary must survive both parsing and formatting unchanged.
func TestQueryLokiLogsPreservesNanosecondBounds(t *testing.T) {
	start, end := time.Unix(1, 1), time.Unix(1, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/loki":
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case "/api/datasources/proxy/uid/loki/loki/api/v1/query_range":
			assert.Equal(t, strconv.FormatInt(start.UnixNano(), 10), r.URL.Query().Get("start"))
			assert.Equal(t, strconv.FormatInt(end.UnixNano(), 10), r.URL.Query().Get("end"))
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"streams","result":[
				{"stream":{"app":"a"},"values":[["1000000002","a2"]]}
			]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 10,
		StartRFC3339: start.UTC().Format(time.RFC3339Nano), EndRFC3339: end.UTC().Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	assert.Equal(t, "a2", result.Data[0].Line)
}
