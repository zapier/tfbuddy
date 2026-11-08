package gitlab_hooks

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sl1pm4t/gongs"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/tfbuddy/pkg/hooks_stream"
)

const metricsTestProject = "zapier/metrics-test"

// newMetricsTestHandler returns a handler whose hook streams publish to an
// in-process JetStream server. With withStream false the HOOKS stream does
// not exist, so every publish fails the way it does while NATS is down.
func newMetricsTestHandler(t *testing.T, withStream bool) *GitlabHooksHandler {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)

	addr, ok := srv.Addr().(*net.TCPAddr)
	require.True(t, ok, "unexpected server address type %T", srv.Addr())
	nc, err := nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", addr.Port))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	require.NoError(t, err)

	if withStream {
		_, err = js.AddStream(&nats.StreamConfig{
			Name:      hooks_stream.HooksStreamName,
			Subjects:  []string{hooks_stream.HooksStreamName + ".>"},
			Retention: nats.WorkQueuePolicy,
			Storage:   nats.MemoryStorage,
		})
		require.NoError(t, err)
	}

	return &GitlabHooksHandler{
		mrStream:    gongs.NewGenericStream[MergeRequestEventMsg](js, mrEventsStreamSubject(), hooks_stream.HooksStreamName),
		notesStream: gongs.NewGenericStream[NoteEventMsg](js, noteEventsStreamSubject(), hooks_stream.HooksStreamName),
	}
}

func postHook(t *testing.T, h *GitlabHooksHandler, eventType gogitlab.EventType, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hooks/gitlab/project", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Gitlab-Event", string(eventType))
	rec := httptest.NewRecorder()
	require.NoError(t, h.handler(echo.New().NewContext(req, rec)))
	// GitLab is answered with 200 OK whatever happens to the event.
	require.Equal(t, http.StatusOK, rec.Code)
}

var (
	mrHookBody   = fmt.Sprintf(`{"object_kind":"merge_request","project":{"path_with_namespace":%q},"object_attributes":{"id":1,"iid":1,"action":"open"}}`, metricsTestProject)
	noteHookBody = fmt.Sprintf(`{"object_kind":"note","project":{"path_with_namespace":%q},"merge_request":{"iid":1},"object_attributes":{"id":2,"discussion_id":"d1","note":"tfc plan"}}`, metricsTestProject)
)

// hookCounts reads the failed counter, which has no project, and the success
// counter for project.
func hookCounts(eventType gogitlab.EventType, reason, project string) (failed, success float64) {
	failed = testutil.ToFloat64(gitlabWebHookFailed.WithLabelValues(string(eventType), reason, ""))
	success = testutil.ToFloat64(gitlabWebHookSuccess.WithLabelValues(string(eventType), "processed", project))
	return failed, success
}

func TestHandlerCountsPublishFailures(t *testing.T) {
	for _, tc := range []struct {
		eventType gogitlab.EventType
		body      string
	}{
		{gogitlab.EventTypeMergeRequest, mrHookBody},
		{gogitlab.EventTypeNote, noteHookBody},
	} {
		t.Run(string(tc.eventType), func(t *testing.T) {
			h := newMetricsTestHandler(t, false)
			failedBefore, successBefore := hookCounts(tc.eventType, hookFailedPublish, metricsTestProject)

			postHook(t, h, tc.eventType, tc.body)

			failedAfter, successAfter := hookCounts(tc.eventType, hookFailedPublish, metricsTestProject)
			require.Equal(t, failedBefore+1, failedAfter, "a failed publish must count as failed")
			require.Equal(t, successBefore, successAfter, "a failed publish must not count as success")
		})
	}
}

func TestHandlerCountsPublishedEvents(t *testing.T) {
	for _, tc := range []struct {
		eventType gogitlab.EventType
		body      string
	}{
		{gogitlab.EventTypeMergeRequest, mrHookBody},
		{gogitlab.EventTypeNote, noteHookBody},
	} {
		t.Run(string(tc.eventType), func(t *testing.T) {
			h := newMetricsTestHandler(t, true)
			failedBefore, successBefore := hookCounts(tc.eventType, hookFailedPublish, metricsTestProject)

			postHook(t, h, tc.eventType, tc.body)

			failedAfter, successAfter := hookCounts(tc.eventType, hookFailedPublish, metricsTestProject)
			require.Equal(t, successBefore+1, successAfter)
			require.Equal(t, failedBefore, failedAfter)
		})
	}
}

func TestHandlerCountsUndecodableEvents(t *testing.T) {
	h := newMetricsTestHandler(t, true)
	before := testutil.ToFloat64(gitlabWebHookFailed.WithLabelValues(string(gogitlab.EventTypeMergeRequest), hookFailedDecode, ""))

	postHook(t, h, gogitlab.EventTypeMergeRequest, `{not json`)

	after := testutil.ToFloat64(gitlabWebHookFailed.WithLabelValues(string(gogitlab.EventTypeMergeRequest), hookFailedDecode, ""))
	require.Equal(t, before+1, after)
}

// rate() ignores the value a series starts with, so the failure series the
// acceptance SLI reads must exist at zero before the first failure.
func TestAcceptanceSeriesExistFromStartup(t *testing.T) {
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	found := map[string]bool{}
	for _, f := range families {
		if f.GetName() != "tfbuddy_gitlab_webhook_failed" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			found[labels["eventType"]+"/"+labels["reason"]+"/"+labels["project"]] = true
		}
	}
	for _, eventType := range []gogitlab.EventType{gogitlab.EventTypeMergeRequest, gogitlab.EventTypeNote} {
		for _, reason := range []string{hookFailedDecode, hookFailedPublish} {
			require.True(t, found[string(eventType)+"/"+reason+"/"], "missing %s/%s", eventType, reason)
		}
	}

	attempts := map[string]bool{}
	for _, f := range families {
		if f.GetName() != "tfbuddy_gitlab_webhook_enqueue_attempts" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				attempts[l.GetValue()] = true
			}
		}
	}
	require.True(t, attempts[string(gogitlab.EventTypeMergeRequest)], "missing merge request enqueue attempts")
	require.True(t, attempts[string(gogitlab.EventTypeNote)], "missing note enqueue attempts")
}

func enqueueAttempts() float64 {
	return testutil.ToFloat64(gitlabWebHookEnqueueAttempts.WithLabelValues(string(gogitlab.EventTypeMergeRequest))) +
		testutil.ToFloat64(gitlabWebHookEnqueueAttempts.WithLabelValues(string(gogitlab.EventTypeNote)))
}

// Every authorized merge request or note event is an enqueue attempt,
// whether it is queued, fails to publish or cannot be decoded: the
// acceptance SLI divides the failures by these attempts.
func TestHandlerCountsEnqueueAttempts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		withStream bool
		eventType  gogitlab.EventType
		body       string
	}{
		{"queued merge request", true, gogitlab.EventTypeMergeRequest, mrHookBody},
		{"queued note", true, gogitlab.EventTypeNote, noteHookBody},
		{"publish failure", false, gogitlab.EventTypeMergeRequest, mrHookBody},
		{"decode failure", true, gogitlab.EventTypeNote, `{not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMetricsTestHandler(t, tc.withStream)
			before := enqueueAttempts()

			postHook(t, h, tc.eventType, tc.body)

			require.Equal(t, before+1, enqueueAttempts())
		})
	}
}

// Rejected requests and ignored event types are not enqueue attempts, so
// they cannot dilute the acceptance SLI.
func TestHandlerDoesNotCountIneligibleRequests(t *testing.T) {
	for _, tc := range []struct {
		name      string
		eventType string
		token     string
		want      int
	}{
		{"ignored event type", "Push Hook", "secret", http.StatusOK},
		{"wrong token", string(gogitlab.EventTypeMergeRequest), "wrong", http.StatusUnauthorized},
		{"missing event header", "", "secret", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMetricsTestHandler(t, true)
			h.hookSecretKey = "secret"
			before := enqueueAttempts()

			req := httptest.NewRequest(http.MethodPost, "/hooks/gitlab/project", strings.NewReader(mrHookBody))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			req.Header.Set(GitlabTokenHeader, tc.token)
			if tc.eventType != "" {
				req.Header.Set("X-Gitlab-Event", tc.eventType)
			}
			rec := httptest.NewRecorder()
			require.NoError(t, h.handler(echo.New().NewContext(req, rec)))
			require.Equal(t, tc.want, rec.Code)

			require.Equal(t, before, enqueueAttempts())
		})
	}
}
