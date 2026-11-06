package gitlab_hooks

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/sl1pm4t/gongs"
	"github.com/stretchr/testify/require"

	"github.com/zapier/tfbuddy/pkg/hooks_stream"
)

func startTestNATS(t *testing.T) nats.JetStreamContext {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	srv := natstest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)
	nc, err := nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", srv.Addr().(*net.TCPAddr).Port))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	require.NoError(t, err)
	return js
}

func counterValue(c prometheus.Counter) float64 {
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		return -1
	}
	return m.GetCounter().GetValue()
}

// newTestMRHandler wires a handler whose MR events go to the HOOKS stream of
// js, without creating the stream.
func newTestMRHandler(js nats.JetStreamContext, policy hooks_stream.RetryPolicy) *GitlabHooksHandler {
	return &GitlabHooksHandler{
		mrStream:     gongs.NewGenericStream[MergeRequestEventMsg](js, mrEventsStreamSubject(), hooks_stream.HooksStreamName),
		publishRetry: policy,
	}
}

func postMREvent(t *testing.T, h *GitlabHooksHandler, project string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"object_kind":"merge_request","project":{"path_with_namespace":%q},"object_attributes":{"iid":7,"action":"open"}}`, project)
	req := httptest.NewRequest(http.MethodPost, "/hooks/gitlab/project", strings.NewReader(body))
	req.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-Gitlab-Event-UUID", "delivery-"+project)
	rec := httptest.NewRecorder()
	require.NoError(t, h.handler(echo.New().NewContext(req, rec)))
	return rec
}

// While NATS can't store the event, for example during a NATS restart, the
// webhook is still answered with 200 straight away, and the event is
// published once the stream is back instead of being dropped.
func TestHandlerPublishesTheEventOnceNATSIsBack(t *testing.T) {
	js := startTestNATS(t)
	h := newTestMRHandler(js, hooks_stream.RetryPolicy{For: 10 * time.Second, InitialInterval: 50 * time.Millisecond, MaxInterval: 200 * time.Millisecond})

	start := time.Now()
	rec := postMREvent(t, h, "zapier/publish-retry")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Less(t, time.Since(start), 3*time.Second, "GitLab gives up on a webhook after 10s")

	_, err := js.AddStream(&nats.StreamConfig{
		Name:      hooks_stream.HooksStreamName,
		Subjects:  []string{hooks_stream.HooksStreamName + ".>"},
		Retention: nats.WorkQueuePolicy,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		info, err := js.StreamInfo(hooks_stream.HooksStreamName)
		return err == nil && info.State.Msgs == 1
	}, 10*time.Second, 50*time.Millisecond, "the event was dropped instead of published once the stream was back")
	require.Eventually(t, func() bool {
		return counterValue(gitlabWebHookSuccess.With(webhookLabels("Merge Request Hook", "zapier/publish-retry", "processed-after-retry"))) == 1
	}, 5*time.Second, 50*time.Millisecond)
}

// An event that can't be published at all is counted as failed, not as
// processed.
func TestHandlerCountsAnEventThatWasNeverPublished(t *testing.T) {
	js := startTestNATS(t)
	h := newTestMRHandler(js, hooks_stream.RetryPolicy{For: 300 * time.Millisecond, InitialInterval: 20 * time.Millisecond, MaxInterval: 50 * time.Millisecond})

	rec := postMREvent(t, h, "zapier/publish-failed")
	require.Equal(t, http.StatusOK, rec.Code)

	failed := gitlabWebHookFailed.With(webhookLabels("Merge Request Hook", "zapier/publish-failed", "publish-failed"))
	require.Eventually(t, func() bool { return counterValue(failed) == 1 }, 5*time.Second, 20*time.Millisecond)
	for _, reason := range []string{"processed", "processed-after-retry"} {
		require.Zero(t, counterValue(gitlabWebHookSuccess.With(webhookLabels("Merge Request Hook", "zapier/publish-failed", reason))), reason)
	}
}
