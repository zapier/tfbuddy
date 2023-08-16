package hooks

import (
	"github.com/heptiolabs/healthcheck"
	"github.com/labstack/echo-contrib/prometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
	"github.com/zapier/tfbuddy/pkg/hooks_stream"
	"github.com/zapier/tfbuddy/pkg/vcs/github"
	"github.com/ziflex/lecho/v3"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"

	"github.com/zapier/tfbuddy/pkg/gitlab_hooks"
	tfnats "github.com/zapier/tfbuddy/pkg/nats"
	"github.com/zapier/tfbuddy/pkg/runstream"
	"github.com/zapier/tfbuddy/pkg/tfc_api"
	"github.com/zapier/tfbuddy/pkg/tfc_hooks"
	ghHooks "github.com/zapier/tfbuddy/pkg/vcs/github/hooks"
	"github.com/zapier/tfbuddy/pkg/vcs/gitlab"
)

func StartServer() {
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Logger = lecho.New(log.Logger)
	// Enable metrics middleware
	p := prometheus.NewPrometheus("echo", nil)
	p.Use(e)

	// add routes
	health := healthcheck.NewHandler()
	e.GET("/ready", echo.WrapHandler(health))
	e.GET("/live", echo.WrapHandler(health))

	// setup NATS client & streams
	nc := tfnats.Connect()
	js, err := nc.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		log.Fatal().Err(err).Msg("could not create Jetstream context")
	}

	hs := hooks_stream.NewHooksStream(nc)
	rs := runstream.NewStream(js)
	health.AddReadinessCheck("nats-connection", tfnats.HealthcheckFn(nc))
	health.AddLivenessCheck("nats-connection", tfnats.HealthcheckFn(nc))
	health.AddLivenessCheck("runstream-streams", rs.HealthCheck)
	health.AddLivenessCheck("hook-stream", hs.HealthCheck)

	// setup API clients
	gl := gitlab.NewGitlabClient()
	tfc := tfc_api.NewTFCClient()

	hooksGroup := e.Group("/hooks")

	// add otel middleware to hooks group
	hooksGroup.Use(otelecho.Middleware("tfbuddy"))

	hooksGroup.Use(middleware.BodyDump(func(c echo.Context, reqBody, resBody []byte) {
		log.Trace().RawJSON("body", reqBody).Msg("Received hook request")
	}))
	logConfig := middleware.DefaultLoggerConfig
	hooksGroup.Use(middleware.LoggerWithConfig(logConfig))

	//
	// Github
	//
	gh := github.NewGithubClient()
	githubHooksHandler := ghHooks.NewGithubHooksHandler(gh, tfc, rs, js)
	hooksGroup.POST("/github/events", githubHooksHandler.Handler)

	//
	// Gitlab
	//
	gitlabGroupHandler := gitlab_hooks.NewGitlabHooksHandler(gl, tfc, rs, js)
	hooksGroup.POST("/gitlab/group", gitlabGroupHandler.GroupHandler())
	hooksGroup.POST("/gitlab/project", gitlabGroupHandler.ProjectHandler())

	//
	// Terraform Cloud
	//
	hooksGroup.POST("/tfc/run_task", tfc_hooks.RunTaskHandler)
	// Run Notifications Handler
	notifHandler := tfc_hooks.NewNotificationHandler(tfc, rs)
	hooksGroup.POST("/tfc/notification", notifHandler.Handler())

	// Github Run Events Processor
	ghep := github.NewRunEventsWorker(gh, rs, tfc)
	defer ghep.Close()

	// Gitlab Run Events Processor
	grsp := gitlab.NewRunStatusProcessor(gl, rs, tfc)
	defer grsp.Close()

	if err := e.Start(":8080"); err != nil {
		log.Fatal().Err(err).Msg("could not start hooks server")
	}

}
