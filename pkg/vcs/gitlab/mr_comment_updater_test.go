package gitlab

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zapier/tfbuddy/pkg/mocks"
	"go.uber.org/mock/gomock"
)

func TestPostComment(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockClient := mocks.NewMockGitClient(mockCtrl)

	tests := []struct {
		name          string
		commentBody   string
		discussionID  string
		setupMocks    func()
		expectedError bool
	}{
		{
			name:         "with discussion ID",
			commentBody:  "test comment",
			discussionID: "disc123",
			setupMocks: func() {
				mockClient.EXPECT().
					AddMergeRequestDiscussionReply(gomock.Any(), 1, "project/path", "disc123", gomock.Any()).
					Return(&GitlabMRNote{}, nil)
			},
			expectedError: false,
		},
		{
			name:         "without discussion ID",
			commentBody:  "test comment",
			discussionID: "",
			setupMocks: func() {
				mockClient.EXPECT().
					CreateMergeRequestComment(gomock.Any(), 1, "project/path", gomock.Any()).
					Return(nil)
			},
			expectedError: false,
		},
		{
			name:         "with error in discussion reply",
			commentBody:  "test comment",
			discussionID: "disc123",
			setupMocks: func() {
				mockClient.EXPECT().
					AddMergeRequestDiscussionReply(gomock.Any(), 1, "project/path", "disc123", gomock.Any()).
					Return(nil, errors.New("API error"))
			},
			expectedError: true,
		},
		{
			name:         "with error in comment creation",
			commentBody:  "test comment",
			discussionID: "",
			setupMocks: func() {
				mockClient.EXPECT().
					CreateMergeRequestComment(gomock.Any(), 1, "project/path", gomock.Any()).
					Return(errors.New("API error"))
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupMocks()

			updater := &RunStatusUpdater{
				client: mockClient,
			}

			err := updater.postComment(context.Background(), tt.commentBody, "project/path", 1, tt.discussionID)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
