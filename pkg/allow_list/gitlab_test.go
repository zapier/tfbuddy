package allow_list

import (
	"os"
	"testing"
)

func TestIsGitlabProjectAllowed(t *testing.T) {
	originalAllowValue, originalAllowExists := os.LookupEnv(GitlabProjectAllowListEnv)
	originalLegacyValue, originalLegacyExists := os.LookupEnv(legacyAllowListEnv)

	t.Cleanup(func() {
		if originalAllowExists {
			os.Setenv(GitlabProjectAllowListEnv, originalAllowValue)
		} else {
			os.Unsetenv(GitlabProjectAllowListEnv)
		}

		if originalLegacyExists {
			os.Setenv(legacyAllowListEnv, originalLegacyValue)
		} else {
			os.Unsetenv(legacyAllowListEnv)
		}
	})

	type args struct {
		projectWithNamespace string
		allowEnv             string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "groupies/repros_allowed",
			args: args{
				projectWithNamespace: "groupies/repros",
				allowEnv:             "groupies,other_groupies",
			},
			want: true,
		},
		{
			name: "other_groupies/repro2_allowed",
			args: args{
				projectWithNamespace: "groupies/repro2",
				allowEnv:             "groupies,other_groupies",
			},
			want: true,
		},
		{
			name: "group/repo_denied",
			args: args{
				projectWithNamespace: "group/repo",
				allowEnv:             "groupies,other_groupies",
			},
			want: false,
		},
		{
			name: "env not set",
			args: args{
				projectWithNamespace: "group/repo",
				allowEnv:             "",
			},
			want: false,
		},
		{
			name: "env single space",
			args: args{
				projectWithNamespace: "group/repo",
				allowEnv:             " ",
			},
			want: false,
		},
		{
			name: "exact match",
			args: args{
				projectWithNamespace: "exact-match/repo",
				allowEnv:             "exact-match/repo",
			},
			want: true,
		},
		{
			name: "partial group match",
			args: args{
				projectWithNamespace: "parent-group/sub-group/repo",
				allowEnv:             "parent-group",
			},
			want: true,
		},
		{
			name: "multiple groups with dashes and underscores",
			args: args{
				projectWithNamespace: "group-with-dash/repo_with_underscore",
				allowEnv:             "group-with-dash,other_group_name",
			},
			want: true,
		},
		{
			name: "complex allow list",
			args: args{
				projectWithNamespace: "team-a/microservice-x",
				allowEnv:             "team-a/microservice,team-b/service,team-c",
			},
			want: true,
		},
		{
			name: "case sensitive match",
			args: args{
				projectWithNamespace: "CaseSensitive/Repo",
				allowEnv:             "CaseSensitive,other",
			},
			want: true,
		},
		{
			name: "case sensitive mismatch",
			args: args{
				projectWithNamespace: "casesensitive/repo",
				allowEnv:             "CaseSensitive,other",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		// Test with primary environment variable
		t.Run(tt.name+"_primary", func(t *testing.T) {
			os.Unsetenv(legacyAllowListEnv)
			os.Setenv(GitlabProjectAllowListEnv, tt.args.allowEnv)

			t.Cleanup(func() {
				os.Unsetenv(GitlabProjectAllowListEnv)
			})

			if got := IsGitlabProjectAllowed(tt.args.projectWithNamespace); got != tt.want {
				t.Errorf("IsGitlabProjectAllowed() with primary env = %v, want %v", got, tt.want)
			}
		})

		// Test with legacy environment variable
		t.Run(tt.name+"_legacy", func(t *testing.T) {
			os.Unsetenv(GitlabProjectAllowListEnv)
			os.Setenv(legacyAllowListEnv, tt.args.allowEnv)

			t.Cleanup(func() {
				os.Unsetenv(legacyAllowListEnv)
			})

			if got := IsGitlabProjectAllowed(tt.args.projectWithNamespace); got != tt.want {
				t.Errorf("IsGitlabProjectAllowed() with legacy env = %v, want %v", got, tt.want)
			}
		})

		// Test fallback behavior (primary empty, using legacy)
		t.Run(tt.name+"_fallback", func(t *testing.T) {
			os.Setenv(GitlabProjectAllowListEnv, "")
			os.Setenv(legacyAllowListEnv, tt.args.allowEnv)

			t.Cleanup(func() {
				os.Unsetenv(GitlabProjectAllowListEnv)
				os.Unsetenv(legacyAllowListEnv)
			})

			if got := IsGitlabProjectAllowed(tt.args.projectWithNamespace); got != tt.want {
				t.Errorf("IsGitlabProjectAllowed() with fallback = %v, want %v", got, tt.want)
			}
		})
	}
}
