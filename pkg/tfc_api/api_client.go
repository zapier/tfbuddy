package tfc_api

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/hashicorp/go-tfe"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

// Defaults match Terraform Cloud's documented per-token limit (~30 req/s).
// When several workspaces fan out concurrently we issue 60+ API calls; without
// a client-side limiter TFC starts returning 429s and runs end up half-created.
const (
	tfcRateLimitRPSKey   = "TFC_RATE_LIMIT_RPS"
	tfcRateLimitBurstKey = "TFC_RATE_LIMIT_BURST"
	defaultTFCRateRPS    = 30
	defaultTFCRateBurst  = 30
)

func init() {
	viper.SetDefault(tfcRateLimitRPSKey, defaultTFCRateRPS)
	viper.SetDefault(tfcRateLimitBurstKey, defaultTFCRateBurst)
}

// rateLimitedTransport wraps an http.RoundTripper with a token-bucket rate
// limiter so concurrent callers cooperatively stay under TFC's per-token
// limit. It is safe for concurrent use.
type rateLimitedTransport struct {
	rt      http.RoundTripper
	limiter *rate.Limiter
}

func (t *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limiter.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.rt.RoundTrip(req)
}

// newRateLimitedHTTPClient returns an http.Client whose transport blocks on
// the supplied limiter. The base transport is a clone of http.DefaultTransport
// so we keep its connection pooling and dial timeouts. Note: the client has no
// top-level Timeout — ConfigurationVersions.Upload streams the cloned repo
// (potentially the whole repo when working_directory is set) and a fixed cap
// would truncate slow uploads; per-call deadlines flow through context.
func newRateLimitedHTTPClient(limiter *rate.Limiter) *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{Transport: &rateLimitedTransport{rt: http.DefaultTransport, limiter: limiter}}
	}
	return &http.Client{Transport: &rateLimitedTransport{rt: base.Clone(), limiter: limiter}}
}

//go:generate mockgen -source api_client.go -destination=../mocks/mock_tfc_api.go -package=mocks github.com/zapier/tfbuddy/pkg/tfc_api
type ApiClient interface {
	GetPlanOutput(id string) ([]byte, error)
	GetRun(ctx context.Context, id string) (*tfe.Run, error)
	GetWorkspaceByName(ctx context.Context, org, name string) (*tfe.Workspace, error)
	GetWorkspaceById(ctx context.Context, id string) (*tfe.Workspace, error)
	CreateRunFromSource(ctx context.Context, opts *ApiRunOptions) (*tfe.Run, error)
	LockUnlockWorkspace(ctx context.Context, workspace string, reason string, tag string, lock bool) error
	AddTags(ctx context.Context, workspace string, prefix string, value string) error
	RemoveTagsByQuery(ctx context.Context, workspace string, query string) error
	RemoveTagsByName(ctx context.Context, workspace string, names []string) error
	GetTagsByQuery(ctx context.Context, workspace string, query string) ([]string, error)
}

type TFCClient struct {
	Client *tfe.Client
}

func NewTFCClient() ApiClient {
	token := os.Getenv("TFC_TOKEN")
	if token == "" {
		log.Fatal().Msg("TFC_TOKEN not set")
	}

	rps := tfcRateLimitValue(tfcRateLimitRPSKey, defaultTFCRateRPS)
	burst := tfcRateLimitValue(tfcRateLimitBurstKey, defaultTFCRateBurst)
	limiter := rate.NewLimiter(rate.Limit(rps), burst)
	log.Info().Int("rps", rps).Int("burst", burst).Msg("TFC client rate limit configured")

	config := &tfe.Config{
		Token:      token,
		HTTPClient: newRateLimitedHTTPClient(limiter),
	}

	tfcClient, err := tfe.NewClient(config)
	if err != nil {
		log.Fatal().Err(err)
	}

	return &TFCClient{Client: tfcClient}
}

// tfcRateLimitValue reads a positive int from viper, falling back (with a
// warning) on any zero/negative value. Garbage strings would already have
// errored out via viper's automatic env binding.
func tfcRateLimitValue(key string, fallback int) int {
	n := viper.GetInt(key)
	if n < 1 {
		log.Warn().Str("key", key).Int("value", n).Int("default", fallback).
			Msg("invalid value, falling back to default")
		return fallback
	}
	return n
}

func (t *TFCClient) GetRun(ctx context.Context, id string) (*tfe.Run, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetTFRun", trace.WithAttributes(attribute.String("run_id", id)))
	defer span.End()

	run, err := t.Client.Runs.ReadWithOptions(
		ctx,
		id,
		&tfe.RunReadOptions{
			Include: []tfe.RunIncludeOpt{tfe.RunPlan, tfe.RunWorkspace, tfe.RunConfigVer, tfe.RunApply},
		},
	)
	if err != nil {
		//log.Error().Err(err)
		return nil, err
	}

	return run, nil
}

func (t *TFCClient) GetPlanOutput(id string) ([]byte, error) {
	b, err := t.Client.Plans.ReadJSONOutput(
		context.Background(),
		id,
	)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (t *TFCClient) GetWorkspaceById(ctx context.Context, id string) (*tfe.Workspace, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetTFWorkspaceById", trace.WithAttributes(attribute.String("run_id", id)))
	defer span.End()
	return t.Client.Workspaces.ReadByID(ctx, id)
}

func (t *TFCClient) GetWorkspaceByName(ctx context.Context, org, name string) (*tfe.Workspace, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetTFWorkspaceByName", trace.WithAttributes(attribute.String("name", name), attribute.String("org", org)))
	defer span.End()
	return t.Client.Workspaces.ReadWithOptions(
		ctx,
		org,
		name,
		&tfe.WorkspaceReadOptions{Include: []tfe.WSIncludeOpt{tfe.WSOrganization}},
	)
}

func (t *TFCClient) LockUnlockWorkspace(ctx context.Context, workspaceID string, reason string, tag string, lock bool) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "LockUnlockWorkspace", trace.WithAttributes(
		attribute.String("workspaceID", workspaceID),
		attribute.Bool("lock", lock),
	))
	defer span.End()
	LockOptions := tfe.WorkspaceLockOptions{Reason: &reason}
	TagPrefix := "gl-lock"

	if lock {
		_, err := t.Client.Workspaces.Lock(
			ctx,
			workspaceID,
			LockOptions,
		)
		log.Info().Msgf("locking workspace")
		if err != nil {
			log.Error().Err(err)
			return err
		}
		err = t.AddTags(ctx, workspaceID, TagPrefix, tag)
		if err != nil {
			log.Error().Err(err)
			return err
		}
	} else {
		_, err := t.Client.Workspaces.Unlock(
			ctx,
			workspaceID,
		)
		log.Info().Msgf("unlocking workspace")
		if err != nil {
			log.Error().Err(err)
			return err
		}
		err = t.RemoveTagsByQuery(ctx, workspaceID, TagPrefix)
		if err != nil {
			log.Error().Err(err)
			return err
		}
	}
	return nil
}

// AddTags Adds a tag to a named terraform workspace. The function returns an error if there's an error generated while trying to add the tags.
// The tags take the format of prefix dash value, which is just a convention and not required by terraform cloud for naming format.
// The tag, however, will be lowercased by terraform cloud, and in any retrieval operations.
func (t *TFCClient) AddTags(ctx context.Context, workspace string, prefix string, value string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "AddTags", trace.WithAttributes(
		attribute.String("workspace", workspace),
	))
	defer span.End()
	LockTag := &tfe.Tag{
		Name: fmt.Sprintf("%s-%s", prefix, value),
	}
	span.SetAttributes(attribute.String("tag", LockTag.Name))

	AddTagsOptions := tfe.WorkspaceAddTagsOptions{
		Tags: []*tfe.Tag{LockTag},
	}

	err := t.Client.Workspaces.AddTags(
		ctx,
		workspace,
		AddTagsOptions,
	)
	return err

}

// RemoveTagsByQuery removes all tags matching a query from a terraform cloud workspace.  It returns an error if one is returned fom searching or removing tags.// Note: the query will match anywhere in the tag, so common substrings should be avoided.
func (t *TFCClient) RemoveTagsByQuery(ctx context.Context, workspace string, query string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "RemoveTagsByQuery", trace.WithAttributes(
		attribute.String("workspace", workspace),
	))
	defer span.End()

	taglist, err := t.GetTagsByQuery(ctx, workspace, query)
	if err != nil {
		log.Error().Err(err)
		return err
	}
	var removeTags []*tfe.Tag
	for _, tag := range taglist {
		retag := tfe.Tag{Name: tag}
		removeTags = append(removeTags, &retag)
	}

	if len(removeTags) == 0 {
		return nil
	}
	RemoveTagsOptions := tfe.WorkspaceRemoveTagsOptions{
		Tags: removeTags,
	}
	err = t.Client.Workspaces.RemoveTags(
		ctx,
		workspace,
		RemoveTagsOptions,
	)
	if err != nil {
		log.Error().Err(err)
		return err
	}
	return nil
}

// RemoveTagsByName removes tags from a workspace by exact name. Unlike RemoveTagsByQuery,
// it does not perform substring matching and is safe to use with values that share common
// substrings (e.g. "tfbuddylock-5" vs "tfbuddylock-50"). Empty input is a no-op.
func (t *TFCClient) RemoveTagsByName(ctx context.Context, workspace string, names []string) error {
	ctx, span := otel.Tracer("TFC").Start(ctx, "RemoveTagsByName", trace.WithAttributes(
		attribute.String("workspace", workspace),
	))
	defer span.End()

	if len(names) == 0 {
		return nil
	}
	tags := make([]*tfe.Tag, 0, len(names))
	for _, name := range names {
		tags = append(tags, &tfe.Tag{Name: name})
	}
	return t.Client.Workspaces.RemoveTags(ctx, workspace, tfe.WorkspaceRemoveTagsOptions{Tags: tags})
}

// GetTagsByQuery returns a list of values of tags on a terraform workspace matching the query string.
// It operates on strings reporesenting the value of the tag and internally converts it to and from the upstreams tag struct as needed.  Attempting to query tags based on their tag ID will not match the tag.
func (t *TFCClient) GetTagsByQuery(ctx context.Context, workspace string, query string) ([]string, error) {
	ctx, span := otel.Tracer("TFC").Start(ctx, "GetTagsByQuery", trace.WithAttributes(
		attribute.String("workspace", workspace),
	))
	defer span.End()

	ListTagOptions := &tfe.WorkspaceTagListOptions{
		Query: &query,
	}
	var tags []string
	TagList, err := t.Client.Workspaces.ListTags(
		ctx,
		workspace,
		ListTagOptions,
	)
	if err != nil {
		log.Error().Err(err)
		return tags, err
	}

	for _, tag := range TagList.Items {
		tags = append(tags, tag.Name)
	}
	return tags, err
}
