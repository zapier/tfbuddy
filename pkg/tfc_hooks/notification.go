package tfc_hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/go-tfe"
	"github.com/kr/pretty"
	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"go.opentelemetry.io/otel"
)

var (
	commonLabels = []string{
		"organization",
		"workspace",
		"status",
	}
	tfcNotificationsReceived = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tfbuddy_tfc_notifications_received",
		Help: "Count of all TFC notification webhooks received",
	},
		[]string{
			"status",
		},
	)
	tfcNotificationPublishSuccess = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tfbuddy_tfc_notifications_success",
		Help: "Count of all TFC Notifications that were processed successfully",
	}, commonLabels)
	tfcNotificationPublishFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tfbuddy_tfc_notifications_failed",
		Help: "Count of all TFC Notifications that could not be processed",
	}, commonLabels)
)

func init() {
	r := prometheus.DefaultRegisterer
	r.MustRegister(tfcNotificationsReceived)
	r.MustRegister(tfcNotificationPublishSuccess)
	r.MustRegister(tfcNotificationPublishFailed)
}

type NotificationHandler struct {
	api    tfc_api.ApiClient
	stream runstream.StreamClient
}

func NewNotificationHandler(api tfc_api.ApiClient, stream runstream.StreamClient) *NotificationHandler {
	h := &NotificationHandler{
		api:    api,
		stream: stream,
	}
	// subscribe to Run Polling Tasks queue
	_, err := stream.SubscribeTFRunPollingTasks(h.pollingStreamCallback)
	if err != nil {
		log.Fatal().Err(err).Msg("could not create Run Polling Task subscription")
	}

	return h
}

func (h *NotificationHandler) Handler() func(c echo.Context) error {
	return func(c echo.Context) error {
		ctx, span := otel.Tracer("TFBuddy").Start(c.Request().Context(), "NotificationHandler")
		defer span.End()

		labels := prometheus.Labels{
			"status": "processed",
		}
		event := NotificationPayload{}

		if err := (&echo.DefaultBinder{}).BindBody(c, &event); err != nil {
			log.Error().Err(err).Msg("failed to unmarshall event payload")
			labels["status"] = "error"
			tfcNotificationsReceived.With(labels).Inc()
			return err
		}
		log.Debug().Str("event", pretty.Sprint(event))

		// do something with event
		h.processNotification(ctx, &event)

		tfcNotificationsReceived.With(labels).Inc()
		return c.String(http.StatusOK, "OK")
	}
}

type NotificationPayload struct {
	PayloadVersion              int       `json:"payload_version"`
	NotificationConfigurationId string    `json:"notification_configuration_id"`
	RunUrl                      string    `json:"run_url"`
	RunId                       string    `json:"run_id"`
	RunMessage                  string    `json:"run_message"`
	RunCreatedAt                time.Time `json:"run_created_at"`
	RunCreatedBy                string    `json:"run_created_by"`
	WorkspaceId                 string    `json:"workspace_id"`
	WorkspaceName               string    `json:"workspace_name"`
	OrganizationName            string    `json:"organization_name"`
	Notifications               []struct {
		Message      string        `json:"message"`
		Trigger      string        `json:"trigger"`
		RunStatus    tfe.RunStatus `json:"run_status"`
		RunUpdatedAt time.Time     `json:"run_updated_at"`
		RunUpdatedBy string        `json:"run_updated_by"`
	} `json:"notifications"`
}

func (h *NotificationHandler) processNotification(ctx context.Context, n *NotificationPayload) {
	ctx, span := otel.Tracer("TFBuddy").Start(ctx, "ProcessNotification")
	defer span.End()

	log.Debug().Interface("NotificationPayload", *n).Msg("processNotification()")
	if n.RunId == "" {
		return
	}
	run, err := h.api.GetRun(ctx, n.RunId)
	if err != nil {
		span.RecordError(err)
		log.Error().Err(err)
	}
	runJson, _ := json.Marshal(run)
	log.Debug().Str("run", string(runJson))
	fmt.Println(string(runJson))

	// notifying
	labels := prometheus.Labels{
		"status":       string(n.Notifications[0].RunStatus),
		"organization": n.OrganizationName,
		"workspace":    n.WorkspaceName,
	}
	err = h.stream.PublishTFRunEvent(ctx, &runstream.TFRunEvent{
		Organization: n.OrganizationName,
		Workspace:    n.WorkspaceName,
		RunID:        n.RunId,
		NewStatus:    string(n.Notifications[0].RunStatus),
	})
	if err != nil {
		span.RecordError(err)
		tfcNotificationPublishFailed.With(labels).Inc()
	} else {
		tfcNotificationPublishSuccess.With(labels).Inc()
	}
}
