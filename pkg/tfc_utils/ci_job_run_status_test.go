package tfc_utils

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/mock/gomock"

	"github.com/zapier/tfbuddy/pkg/mocks"
)

func TestWaitForRunCompletionOrFailure_continuesOnGetRunError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	origInterval := retryInterval
	retryInterval = 1 * time.Millisecond
	defer func() { retryInterval = origInterval }()

	origClient := tfcClient
	mockTFC := mocks.NewMockApiClient(ctrl)
	tfcClient = mockTFC
	defer func() { tfcClient = origClient }()

	// All GetRun calls return an error — the loop must continue, not panic.
	mockTFC.EXPECT().GetRun(gomock.Any(), "run-123").Return(nil, errors.New("transient")).AnyTimes()

	wg := &sync.WaitGroup{}
	wg.Add(1)
	// Should complete without panicking.
	waitForRunCompletionOrFailure(context.Background(), wg, &gogitlab.CommitStatus{}, "ws", "run-123")
}
