package allow_list

import (
	"reflect"
	"testing"
)

func TestGetAllowList(t *testing.T) {
	const testEnvVar = "TEST_ALLOW_LIST_ENV"

	tests := []struct {
		name   string
		envVal string
		want   []string
	}{
		{
			name:   "multiple values",
			envVal: "prefix1,prefix2,prefix3",
			want:   []string{"prefix1", "prefix2", "prefix3"},
		},
		{
			name:   "single value",
			envVal: "prefix1",
			want:   []string{"prefix1"},
		},
		{
			name:   "values with spaces",
			envVal: " prefix1 , prefix2 , prefix3 ",
			want:   []string{"prefix1", "prefix2", "prefix3"},
		},
		{
			name:   "empty env",
			envVal: "",
			want:   nil,
		},
		{
			name:   "only spaces",
			envVal: "   ",
			want:   nil,
		},
		{
			name:   "empty strings in list",
			envVal: "prefix1,,prefix3",
			want:   []string{"prefix1", "prefix3"},
		},
		{
			name:   "trailing comma",
			envVal: "prefix1,prefix2,",
			want:   []string{"prefix1", "prefix2"},
		},
		{
			name:   "leading comma",
			envVal: ",prefix1,prefix2",
			want:   []string{"prefix1", "prefix2"},
		},
		{
			name:   "multiple consecutive commas",
			envVal: "prefix1,,,prefix2",
			want:   []string{"prefix1", "prefix2"},
		},
		{
			name:   "only commas",
			envVal: ",,,",
			want:   nil,
		},
		{
			name:   "values with forward slashes",
			envVal: "zapier/,github/org,gitlab/project",
			want:   []string{"zapier/", "github/org", "gitlab/project"},
		},
		{
			name:   "values with special characters",
			envVal: "org-name,project_name,repo.name,user@domain",
			want:   []string{"org-name", "project_name", "repo.name", "user@domain"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(testEnvVar, tt.envVal)

			if got := getAllowList(testEnvVar); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getAllowList() = %v, want %v", got, tt.want)
			}
		})
	}
}
