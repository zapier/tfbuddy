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
