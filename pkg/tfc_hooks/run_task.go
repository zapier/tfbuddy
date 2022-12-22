package tfc_hooks

import (
	"github.com/kr/pretty"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
	"net/http"
	"time"
)

func RunTaskHandler(c echo.Context) error {
	event := RunTaskEvent{}

	if err := (&echo.DefaultBinder{}).BindBody(c, &event); err != nil {
		log.Error().Err(err).Msg("failed to unmarshall event payload")
		return err
	}

	log.Debug().Str("event", pretty.Sprint(event))

	// do something with event

	// send ack to TFC RunAppUrl

	return c.String(http.StatusOK, "OK")
}

type RunTaskEvent struct {
	PayloadVersion             int         `json:"payload_version"`
	AccessToken                string      `json:"access_token"`
	TaskResultId               string      `json:"task_result_id"`
	TaskResultEnforcementLevel string      `json:"task_result_enforcement_level"`
	TaskResultCallbackUrl      string      `json:"task_result_callback_url"`
	RunAppUrl                  string      `json:"run_app_url"`
	RunId                      string      `json:"run_id"`
	RunMessage                 string      `json:"run_message"`
	RunCreatedAt               time.Time   `json:"run_created_at"`
	RunCreatedBy               string      `json:"run_created_by"`
	WorkspaceId                string      `json:"workspace_id"`
	WorkspaceName              string      `json:"workspace_name"`
	WorkspaceAppUrl            string      `json:"workspace_app_url"`
	OrganizationName           string      `json:"organization_name"`
	PlanJsonApiUrl             string      `json:"plan_json_api_url"`
	VcsRepoUrl                 string      `json:"vcs_repo_url"`
	VcsBranch                  string      `json:"vcs_branch"`
	VcsPullRequestUrl          interface{} `json:"vcs_pull_request_url"`
	VcsCommitUrl               string      `json:"vcs_commit_url"`
}

type RunTaskCallback struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Url     string `json:"url"`
		} `json:"attributes"`
	} `json:"data"`
}
