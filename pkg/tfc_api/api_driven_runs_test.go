package tfc_api

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/hashicorp/go-tfe"
)

// fakeCV implements tfe.ConfigurationVersions with configurable per-call results.
type fakeCV struct {
	calls   int
	results []cvResult
}

type cvResult struct {
	cv  *tfe.ConfigurationVersion
	err error
}

func (f *fakeCV) Read(ctx context.Context, cvID string) (*tfe.ConfigurationVersion, error) {
	i := f.calls
	f.calls++
	if i < len(f.results) {
		return f.results[i].cv, f.results[i].err
	}
	return nil, errors.New("fakeCV: unexpected extra call")
}

func (f *fakeCV) List(_ context.Context, _ string, _ *tfe.ConfigurationVersionListOptions) (*tfe.ConfigurationVersionList, error) {
	panic("unexpected call")
}
func (f *fakeCV) Create(_ context.Context, _ string, _ tfe.ConfigurationVersionCreateOptions) (*tfe.ConfigurationVersion, error) {
	panic("unexpected call")
}
func (f *fakeCV) CreateForRegistryModule(_ context.Context, _ tfe.RegistryModuleID) (*tfe.ConfigurationVersion, error) {
	panic("unexpected call")
}
func (f *fakeCV) ReadWithOptions(_ context.Context, _ string, _ *tfe.ConfigurationVersionReadOptions) (*tfe.ConfigurationVersion, error) {
	panic("unexpected call")
}
func (f *fakeCV) Upload(_ context.Context, _ string, _ string) error  { panic("unexpected call") }
func (f *fakeCV) UploadTarGzip(_ context.Context, _ string, _ io.Reader) error {
	panic("unexpected call")
}
func (f *fakeCV) Archive(_ context.Context, _ string) error                    { panic("unexpected call") }
func (f *fakeCV) Download(_ context.Context, _ string) ([]byte, error)         { panic("unexpected call") }
func (f *fakeCV) SoftDeleteBackingData(_ context.Context, _ string) error      { panic("unexpected call") }
func (f *fakeCV) RestoreBackingData(_ context.Context, _ string) error         { panic("unexpected call") }
func (f *fakeCV) PermanentlyDeleteBackingData(_ context.Context, _ string) error {
	panic("unexpected call")
}

func TestPollCVWhilePending_continuesOnTransientError(t *testing.T) {
	fake := &fakeCV{
		results: []cvResult{
			{nil, errors.New("transient network error")},
			{&tfe.ConfigurationVersion{Status: tfe.ConfigurationUploaded}, nil},
		},
	}

	client := &TFCClient{Client: &tfe.Client{ConfigurationVersions: fake}}
	cv := &tfe.ConfigurationVersion{ID: "cv-abc123"}

	result, err := client.pollCVWhilePending(context.Background(), cv)

	if err != nil {
		t.Fatalf("expected no error after transient failure, got: %v", err)
	}
	if result.Status != tfe.ConfigurationUploaded {
		t.Fatalf("expected ConfigurationUploaded, got: %v", result.Status)
	}
	if fake.calls != 2 {
		t.Fatalf("expected 2 Read calls, got: %d", fake.calls)
	}
}
