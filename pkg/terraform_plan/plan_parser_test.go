package terraform_plan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/stretchr/testify/assert"
)

var updateGolden = false

func init() {
	updateGoldenEnv := os.Getenv("TFBUDDY_TEST_UPDATE_GOLDEN")
	if updateGoldenEnv == "true" {
		updateGolden = true
	}
}

func Test_parseJSONPlan(t *testing.T) {

	tests := []struct {
		name    string
		tfplan  string
		want    *tfjson.Plan
		wantErr bool
	}{
		{
			name:    "basic",
			tfplan:  "testdata/TestPresentPlanChangesAsMarkdown/basic.tfplan.json",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "destroy-create",
			tfplan:  "testdata/TestPresentPlanChangesAsMarkdown/destroy-create.tfplan.json",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "import",
			tfplan:  "testdata/TestPresentPlanChangesAsMarkdown/import.tfplan.json",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			tfplan:  "",
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var planFile []byte
			if tt.tfplan != "" {
				planFile = testLoadFile(t, tt.tfplan)
			} else {
				planFile = []byte("invalid json")
			}
			_, err := parseJSONPlan(planFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseJSONPlan() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

		})
	}
}

func TestPresentPlanChangesAsMarkdown(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "basic",
		},
		{
			name: "destroy-create",
		},
		{
			name: "replace",
		},
		{
			name: "import",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := testLoadTestData(t, ".tfplan.json")
			got := PresentPlanChangesAsMarkdown(plan, "http://app.terraform.io/x/y/z")
			if updateGolden {
				testWriteTestData(t, ".md", []byte(got))
			}
			want := string(testLoadTestData(t, ".md"))
			assert.Equal(t, want, got, "")
		})
	}
}

func TestPresentPlanChangesAsMarkdown_EdgeCases(t *testing.T) {
	t.Run("empty plan", func(t *testing.T) {
		emptyPlan := `{"format_version":"1.2","terraform_version":"1.0.0","resource_changes":[]}`
		got := PresentPlanChangesAsMarkdown([]byte(emptyPlan), "http://example.com")
		assert.Contains(t, got, "0 to import, 0 to add, 0 to change, 0 to replace and 0 to destroy")
	})

	t.Run("invalid JSON returns empty string", func(t *testing.T) {
		got := PresentPlanChangesAsMarkdown([]byte("invalid json"), "http://example.com")
		assert.Equal(t, "", got)
	})
}

func Test_processChanges(t *testing.T) {
	tests := []struct {
		name     string
		change   *tfjson.ResourceChange
		expected int
	}{
		{
			name: "basic update with changes",
			change: &tfjson.ResourceChange{
				Change: &tfjson.Change{
					Actions:      tfjson.Actions{tfjson.ActionUpdate},
					Before:       map[string]interface{}{"name": "old", "count": 1},
					After:        map[string]interface{}{"name": "new", "count": 2},
					AfterUnknown: map[string]interface{}{},
				},
			},
			expected: 2,
		},
		{
			name: "create action",
			change: &tfjson.ResourceChange{
				Change: &tfjson.Change{
					Actions:      tfjson.Actions{tfjson.ActionCreate},
					Before:       map[string]interface{}{"name": "test"},
					After:        map[string]interface{}{"name": "test"},
					AfterUnknown: map[string]interface{}{},
				},
			},
			expected: 1,
		},
		{
			name: "with sensitive values",
			change: &tfjson.ResourceChange{
				Change: &tfjson.Change{
					Actions:        tfjson.Actions{tfjson.ActionUpdate},
					Before:         map[string]interface{}{"password": "old", "name": "test"},
					After:          map[string]interface{}{"password": "new", "name": "test"},
					AfterSensitive: map[string]interface{}{"password": true},
					AfterUnknown:   map[string]interface{}{},
				},
			},
			expected: 1,
		},
		{
			name: "with unknown values",
			change: &tfjson.ResourceChange{
				Change: &tfjson.Change{
					Actions:      tfjson.Actions{tfjson.ActionUpdate},
					Before:       map[string]interface{}{"id": "old", "name": "test"},
					After:        map[string]interface{}{"id": "new", "name": "test"},
					AfterUnknown: map[string]interface{}{"id": true},
				},
			},
			expected: 1,
		},
		{
			name: "mixed field changes",
			change: &tfjson.ResourceChange{
				Change: &tfjson.Change{
					Actions:      tfjson.Actions{tfjson.ActionUpdate},
					Before:       map[string]interface{}{"name": "test", "value": "same"},
					After:        map[string]interface{}{"name": "changed", "value": "same"},
					AfterUnknown: map[string]interface{}{},
				},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := processChanges(tt.change)
			assert.Len(t, changes, tt.expected)
		})
	}
}

func Test_isReplace(t *testing.T) {
	tests := []struct {
		name     string
		change   *tfjson.ResourceChange
		expected bool
	}{
		{
			name: "replace action",
			change: &tfjson.ResourceChange{
				Change: &tfjson.Change{
					Actions: tfjson.Actions{tfjson.ActionDelete, tfjson.ActionCreate},
				},
			},
			expected: true,
		},
		{
			name: "single action",
			change: &tfjson.ResourceChange{
				Change: &tfjson.Change{
					Actions: tfjson.Actions{tfjson.ActionUpdate},
				},
			},
			expected: false,
		},
		{
			name: "empty actions",
			change: &tfjson.ResourceChange{
				Change: &tfjson.Change{
					Actions: tfjson.Actions{},
				},
			},
			expected: false,
		},
		{
			name: "multiple non-replace actions",
			change: &tfjson.ResourceChange{
				Change: &tfjson.Change{
					Actions: tfjson.Actions{tfjson.ActionUpdate, tfjson.ActionNoop},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isReplace(tt.change)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func testLoadTestData(t *testing.T, suffix string) []byte {
	filename := fmt.Sprintf("testdata/%s%s", t.Name(), suffix)
	return testLoadFile(t, filename)
}

func testLoadFile(t *testing.T, filename string) []byte {
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("could not read test file (%s): %v", filename, err)
	}
	return data
}

func testWriteTestData(t *testing.T, suffix string, b []byte) {
	filename := fmt.Sprintf("testdata/%s%s", t.Name(), suffix)
	err := os.MkdirAll(filepath.Dir(filename), 0755)
	if err != nil {
		t.Fatalf("could not create dir (%s): %v", filename, err)
	}
	err = os.WriteFile(filename, b, 0644)
	if err != nil {
		t.Fatalf("could not write testdata file (%s): %v", filename, err)
	}
}
