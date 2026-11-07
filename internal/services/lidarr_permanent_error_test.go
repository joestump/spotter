// Governing: issue #331 (typed permanent/transient split), SPEC-0017 REQ "Backoff Strategy"
package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"spotter/internal/resilience"
)

// TestIsPermanentLidarrErrorReadsStatusNotProse holds the classification to
// the rule that a status Lidarr actually returned decides the permanent
// question, so a reworded message cannot put a rejected submission back into
// the retry loop. Prose is only consulted when there is no status to read.
func TestIsPermanentLidarrErrorReadsStatusNotProse(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "400 with prose no fragment list knows",
			err:  resilience.NewHTTPStatusError(http.StatusBadRequest, errors.New(`lidarr api error: 400 - {"validationFailures":[{"message":"Lidarr words this differently now"}]}`)),
			want: true,
		},
		{
			name: "422 wrapped the way submitArtist wraps it",
			err:  fmt.Errorf("failed to add artist: %w", resilience.NewHTTPStatusError(http.StatusUnprocessableEntity, errors.New("lidarr api error: 422 - whatever the validator says today"))),
			want: true,
		},
		{
			name: "401 credentials",
			err:  resilience.NewHTTPStatusError(http.StatusUnauthorized, errors.New("lidarr api error: 401 - nope")),
			want: true,
		},
		{
			name: "500 with reworded prose stays transient",
			err:  resilience.NewHTTPStatusError(http.StatusInternalServerError, errors.New("lidarr api error: 500 - unexpected condition")),
			want: false,
		},
		{
			name: "429 stays transient",
			err:  resilience.NewHTTPStatusError(http.StatusTooManyRequests, errors.New("lidarr api error: 429 - slow down")),
			want: false,
		},
		{
			name: "404 stays transient, reverse proxies flap during a redeploy",
			err:  resilience.NewHTTPStatusError(http.StatusNotFound, errors.New("lidarr api error: 404 - no route")),
			want: false,
		},
		{
			name: "Spotter's own refusal without a status still matches on text",
			err:  errors.New("album 7 does not belong to the queue owner (user 3)"),
			want: true,
		},
		{
			name: "an entity missing from a lookup is permanent",
			err:  fmt.Errorf("artist lookup failed: %w", errors.New("artist not found in lidarr lookup for mbid abc")),
			want: true,
		},
		{
			name: "a transport failure is transient",
			err:  fmt.Errorf("artist lookup failed: %w", context.DeadlineExceeded),
			want: false,
		},
		{
			name: "no error at all",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		if got := isPermanentLidarrError(tc.err); got != tc.want {
			t.Errorf("%s: isPermanentLidarrError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestLidarrSubmitterRequestErrorsCarryTypedStatus proves the submitter's HTTP
// failures reach the classifier typed, which is what keeps its permanent
// decision off error prose.
func TestLidarrSubmitterRequestErrorsCarryTypedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"Something Lidarr reworded"}`))
	}))
	defer server.Close()

	s := NewLidarrSubmitter(nil, lidarrConfig(server.URL), slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := s.doRequest(context.Background(), "POST", "artist", map[string]string{"title": "test"}, nil)
	if err == nil {
		t.Fatal("expected the 422 to be reported")
	}

	var statusErr *resilience.HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected a typed status error, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", statusErr.StatusCode)
	}
	if !isPermanentLidarrError(err) {
		t.Error("a 422 refusal should be permanent regardless of its wording")
	}
}
