package frontend

import (
	"bytes"
	"context"
	"io/ioutil"
	"net/http"
	"strconv"
	"testing"

	jsoniter "github.com/json-iterator/go"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/weaveworks/common/httpgrpc"
	"github.com/weaveworks/common/user"

	"github.com/cortexproject/cortex/pkg/ingester/client"
)

const (
	query        = "/api/v1/query_range?end=1536716898&query=sum%28container_memory_rss%29+by+%28namespace%29&start=1536673680&step=120"
	responseBody = `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"foo":"bar"},"values":[[1536673680,"137"],[1536673780,"137"]]}]}}`
)

var (
	parsedRequest = &QueryRangeRequest{
		Path:  "/api/v1/query_range",
		Start: 1536673680 * 1e3,
		End:   1536716898 * 1e3,
		Step:  120 * 1e3,
		Query: "sum(container_memory_rss) by (namespace)",
	}
	parsedResponse = &APIResponse{
		Status: "success",
		Data: QueryRangeResponse{
			ResultType: model.ValMatrix.String(),
			Result: []SampleStream{
				{
					Labels: []client.LabelAdapter{
						{Name: "foo", Value: "bar"},
					},
					Samples: []client.Sample{
						{Value: 137, TimestampMs: 1536673680000},
						{Value: 137, TimestampMs: 1536673780000},
					},
				},
			},
		},
	}
)

func TestQueryRangeRequest(t *testing.T) {
	for i, tc := range []struct {
		url         string
		expected    *QueryRangeRequest
		expectedErr error
	}{
		{
			url:      query,
			expected: parsedRequest,
		},
		{
			url:         "api/v1/query_range?start=foo",
			expectedErr: httpgrpc.Errorf(http.StatusBadRequest, "cannot parse \"foo\" to a valid timestamp"),
		},
		{
			url:         "api/v1/query_range?start=123&end=bar",
			expectedErr: httpgrpc.Errorf(http.StatusBadRequest, "cannot parse \"bar\" to a valid timestamp"),
		},
		{
			url:         "api/v1/query_range?start=123&end=0",
			expectedErr: errEndBeforeStart,
		},
		{
			url:         "api/v1/query_range?start=123&end=456&step=baz",
			expectedErr: httpgrpc.Errorf(http.StatusBadRequest, "cannot parse \"baz\" to a valid duration"),
		},
		{
			url:         "api/v1/query_range?start=123&end=456&step=-1",
			expectedErr: errNegativeStep,
		},
		{
			url:         "api/v1/query_range?start=0&end=11001&step=1",
			expectedErr: errStepTooSmall,
		},
	} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			r, err := http.NewRequest("GET", tc.url, nil)
			require.NoError(t, err)

			ctx := user.InjectOrgID(context.Background(), "1")
			r = r.WithContext(ctx)

			req, err := parseQueryRangeRequest(r)
			if err != nil {
				require.EqualValues(t, tc.expectedErr, err)
				return
			}
			require.EqualValues(t, tc.expected, req)

			rdash, err := req.toHTTPRequest(context.Background())
			require.NoError(t, err)
			require.EqualValues(t, tc.url, rdash.RequestURI)
		})
	}
}

func TestQueryRangeResponse(t *testing.T) {
	for i, tc := range []struct {
		body     string
		expected *APIResponse
	}{
		{
			body:     responseBody,
			expected: parsedResponse,
		},
	} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			response := &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       ioutil.NopCloser(bytes.NewBuffer([]byte(tc.body))),
			}
			resp, err := parseQueryRangeResponse(context.Background(), response)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, resp)

			// Reset response, as the above call will have consumed the body reader.
			response = &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       ioutil.NopCloser(bytes.NewBuffer([]byte(tc.body))),
			}
			resp2, err := resp.toHTTPResponse(context.Background())
			require.NoError(t, err)
			assert.Equal(t, response, resp2)
		})
	}
}

func TestMergeAPIResponses(t *testing.T) {
	for i, tc := range []struct {
		input    []*APIResponse
		expected *APIResponse
	}{
		// No responses shouldn't panic.
		{
			input: []*APIResponse{},
			expected: &APIResponse{
				Status: statusSuccess,
			},
		},

		// A single empty response shouldn't panic.
		{
			input: []*APIResponse{
				{
					Data: QueryRangeResponse{
						ResultType: matrix,
						Result:     []SampleStream{},
					},
				},
			},
			expected: &APIResponse{
				Status: statusSuccess,
				Data: QueryRangeResponse{
					ResultType: matrix,
					Result:     []SampleStream{},
				},
			},
		},

		// Multiple empty responses shouldn't panic.
		{
			input: []*APIResponse{
				{
					Data: QueryRangeResponse{
						ResultType: matrix,
						Result:     []SampleStream{},
					},
				},
				{
					Data: QueryRangeResponse{
						ResultType: matrix,
						Result:     []SampleStream{},
					},
				},
			},
			expected: &APIResponse{
				Status: statusSuccess,
				Data: QueryRangeResponse{
					ResultType: matrix,
					Result:     []SampleStream{},
				},
			},
		},

		// Basic merging of two responses.
		{
			input: []*APIResponse{
				{
					Data: QueryRangeResponse{
						ResultType: matrix,
						Result: []SampleStream{
							{
								Labels: []client.LabelAdapter{},
								Samples: []client.Sample{
									{Value: 0, TimestampMs: 0},
									{Value: 1, TimestampMs: 1},
								},
							},
						},
					},
				},
				{
					Data: QueryRangeResponse{
						ResultType: matrix,
						Result: []SampleStream{
							{
								Labels: []client.LabelAdapter{},
								Samples: []client.Sample{
									{Value: 2, TimestampMs: 2},
									{Value: 3, TimestampMs: 3},
								},
							},
						},
					},
				},
			},
			expected: &APIResponse{
				Status: statusSuccess,
				Data: QueryRangeResponse{
					ResultType: matrix,
					Result: []SampleStream{
						{
							Labels: []client.LabelAdapter{},
							Samples: []client.Sample{
								{Value: 0, TimestampMs: 0},
								{Value: 1, TimestampMs: 1},
								{Value: 2, TimestampMs: 2},
								{Value: 3, TimestampMs: 3},
							},
						},
					},
				},
			},
		},

		// Merging of responses when labels are in different order.
		{
			input: []*APIResponse{
				mustParse(t, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"a":"b","c":"d"},"values":[[0,"0"],[1,"1"]]}]}}`),
				mustParse(t, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"c":"d","a":"b"},"values":[[2,"2"],[3,"3"]]}]}}`),
			},
			expected: &APIResponse{
				Status: statusSuccess,
				Data: QueryRangeResponse{
					ResultType: matrix,
					Result: []SampleStream{
						{
							Labels: []client.LabelAdapter{{Name: "a", Value: "b"}, {Name: "c", Value: "d"}},
							Samples: []client.Sample{
								{Value: 0, TimestampMs: 0},
								{Value: 1, TimestampMs: 1000},
								{Value: 2, TimestampMs: 2000},
								{Value: 3, TimestampMs: 3000},
							},
						},
					},
				},
			},
		},
		// Merging of samples where there is overlap.
		{
			input: []*APIResponse{
				mustParse(t, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"a":"b","c":"d"},"values":[[1,"1"],[2,"2"]]}]}}`),
				mustParse(t, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"c":"d","a":"b"},"values":[[2,"2"],[3,"3"]]}]}}`),
			},
			expected: &APIResponse{
				Status: statusSuccess,
				Data: QueryRangeResponse{
					ResultType: matrix,
					Result: []SampleStream{
						{
							Labels: []client.LabelAdapter{{Name: "a", Value: "b"}, {Name: "c", Value: "d"}},
							Samples: []client.Sample{
								{Value: 1, TimestampMs: 1000},
								{Value: 2, TimestampMs: 2000},
								{Value: 3, TimestampMs: 3000},
							},
						},
					},
				},
			},
		}} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			output, err := mergeAPIResponses(tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.expected, output)
		})
	}
}

func mustParse(t *testing.T, apiResponse string) *APIResponse {
	var resp APIResponse
	// Needed as goimports automatically add a json import otherwise.
	json := jsoniter.ConfigCompatibleWithStandardLibrary
	require.NoError(t, json.Unmarshal([]byte(apiResponse), &resp))
	return &resp
}
