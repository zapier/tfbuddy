package allow_list

import (
	"os"
	"testing"
)

func TestIsGithubRepoAllowed(t *testing.T) {
	originalValue, originalExists := os.LookupEnv(githubRepoAllowListEnv)

	t.Cleanup(func() {
		if originalExists {
			os.Setenv(githubRepoAllowListEnv, originalValue)
		} else {
			os.Unsetenv(githubRepoAllowListEnv)
		}
	})

	type args struct {
		fullName string
		allowEnv string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "org/repo_allowed",
			args: args{
				fullName: "org/repo",
				allowEnv: "org,other_org",
			},
			want: true,
		},
		{
			name: "org/other_repo_allowed",
			args: args{
				fullName: "org/other_repo",
				allowEnv: "org,other_org",
			},
			want: true,
		},
		{
			name: "different_org/repo_denied",
			args: args{
				fullName: "different_org/repo",
				allowEnv: "org,other_org",
			},
			want: false,
		},
		{
			name: "env not set",
			args: args{
				fullName: "org/repo",
				allowEnv: "",
			},
			want: false,
		},
		{
			name: "env single space",
			args: args{
				fullName: "org/repo",
				allowEnv: " ",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(githubRepoAllowListEnv, tt.args.allowEnv)

			t.Cleanup(func() {
				os.Unsetenv(githubRepoAllowListEnv)
			})

			if got := IsGithubRepoAllowed(tt.args.fullName); got != tt.want {
				t.Errorf("IsGithubRepoAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}
