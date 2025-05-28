package allow_list

import (
	"testing"
)

func TestIsGitlabProjectAllowed(t *testing.T) {
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
			name: "specific project allowed",
			args: args{
				projectWithNamespace: "groupies/specific-project",
				allowEnv:             "groupies/specific-project,other_groupies",
			},
			want: true,
		},
		{
			name: "project not in specific list",
			args: args{
				projectWithNamespace: "groupies/other-project",
				allowEnv:             "groupies/specific-project,other_groupies/project",
			},
			want: false,
		},
		{
			name: "partial match allowed with prefix",
			args: args{
				projectWithNamespace: "groupiesprefix/project",
				allowEnv:             "groupies,other_groupies",
			},
			want: true,
		},
		{
			name: "spaces in allow list",
			args: args{
				projectWithNamespace: "groupies/project",
				allowEnv:             "groupies, other_groupies",
			},
			want: true,
		},
		{
			name: "trailing spaces",
			args: args{
				projectWithNamespace: "other_groupies/project",
				allowEnv:             "groupies,other_groupies ",
			},
			want: true,
		},
		{
			name: "case sensitivity",
			args: args{
				projectWithNamespace: "Groupies/Project",
				allowEnv:             "groupies",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_primary", func(t *testing.T) {
			t.Setenv(legacyAllowListEnv, "")
			t.Setenv(GitlabProjectAllowListEnv, tt.args.allowEnv)

			if got := IsGitlabProjectAllowed(tt.args.projectWithNamespace); got != tt.want {
				t.Errorf("IsGitlabProjectAllowed() with primary env = %v, want %v", got, tt.want)
			}
		})

		t.Run(tt.name+"_legacy", func(t *testing.T) {
			t.Setenv(GitlabProjectAllowListEnv, "")
			t.Setenv(legacyAllowListEnv, tt.args.allowEnv)

			if got := IsGitlabProjectAllowed(tt.args.projectWithNamespace); got != tt.want {
				t.Errorf("IsGitlabProjectAllowed() with legacy env = %v, want %v", got, tt.want)
			}
		})
	}
}
