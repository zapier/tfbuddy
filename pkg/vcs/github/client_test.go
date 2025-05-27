package github

import (
	"testing"

	gogithub "github.com/google/go-github/v69/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zapier/tfbuddy/pkg/utils"
)

func TestSplitFullName(t *testing.T) {
	tests := []struct {
		name     string
		fullName string
		expected []string
		hasError bool
	}{
		{
			name:     "valid repo format",
			fullName: "owner/repo",
			expected: []string{"owner", "repo"},
			hasError: false,
		},
		{
			name:     "invalid repo format - no slash",
			fullName: "ownerrepo",
			expected: nil,
			hasError: true,
		},
		{
			name:     "invalid repo format - multiple slashes",
			fullName: "owner/repo/extra",
			expected: nil,
			hasError: true,
		},
		{
			name:     "empty string",
			fullName: "",
			expected: nil,
			hasError: true,
		},
		{
			name:     "only slash",
			fullName: "/",
			expected: []string{"", ""},
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := splitFullName(tt.fullName)

			if tt.hasError {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestString(t *testing.T) {
	input := "test string"
	result := String(input)

	require.NotNil(t, result)
	assert.Equal(t, input, *result)
}

func TestResolveOwnerName(t *testing.T) {
	tests := []struct {
		name     string
		owner    *gogithub.User
		expected string
		hasError bool
	}{
		{
			name: "owner with name",
			owner: &gogithub.User{
				Name: String("John Doe"),
			},
			expected: "John Doe",
			hasError: false,
		},
		{
			name: "owner with login only",
			owner: &gogithub.User{
				Login: String("johndoe"),
			},
			expected: "johndoe",
			hasError: false,
		},
		{
			name: "owner with both name and login",
			owner: &gogithub.User{
				Name:  String("John Doe"),
				Login: String("johndoe"),
			},
			expected: "John Doe",
			hasError: false,
		},
		{
			name:     "owner with nil name and login",
			owner:    &gogithub.User{},
			expected: "",
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveOwnerName(tt.owner)

			if tt.hasError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "owner name/login is nil")
				assert.ErrorIs(t, err, utils.ErrPermanent)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestCreateBackOffWithRetries(t *testing.T) {
	backoff := createBackOffWithRetries()
	assert.NotNil(t, backoff)
	// The backoff should be configured properly
	// This is a simple test to ensure the function doesn't panic
}
