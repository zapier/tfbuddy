package utils

import (
	"testing"
)

func TestCaptureSubstring(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		prefix string
		suffix string
		want   string
	}{
		{
			name:   "basic capture",
			body:   "some text **Run URL**: https://example.com <br> more text",
			prefix: "**Run URL**: ",
			suffix: " <br>",
			want:   "https://example.com",
		},
		{
			name:   "prefix not found",
			body:   "some text without prefix",
			prefix: "**Run URL**: ",
			suffix: " <br>",
			want:   "",
		},
		{
			name:   "suffix not found",
			body:   "some text **Run URL**: https://example.com more text",
			prefix: "**Run URL**: ",
			suffix: " <br>",
			want:   "",
		},
		{
			name:   "empty body",
			body:   "",
			prefix: "**Run URL**: ",
			suffix: " <br>",
			want:   "",
		},
		{
			name:   "prefix at start",
			body:   "**Run URL**: https://example.com <br>",
			prefix: "**Run URL**: ",
			suffix: " <br>",
			want:   "https://example.com",
		},
		{
			name:   "multiple occurrences - captures first",
			body:   "**Run URL**: first.com <br> text **Run URL**: second.com <br>",
			prefix: "**Run URL**: ",
			suffix: " <br>",
			want:   "first.com",
		},
		{
			name:   "capture status",
			body:   "some text **Status**: `planned` more text",
			prefix: "**Status**: ",
			suffix: " more",
			want:   "`planned`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CaptureSubstring(tt.body, tt.prefix, tt.suffix); got != tt.want {
				t.Errorf("CaptureSubstring() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
	}{
		{
			name:   "applied status",
			status: "applied",
			want:   "✅ Applied",
		},
		{
			name:   "planned status",
			status: "planned",
			want:   "📝 Planned",
		},
		{
			name:   "policy_checked status",
			status: "policy_checked",
			want:   "📝 Policy Checked",
		},
		{
			name:   "policy_soft_failed status",
			status: "policy_soft_failed",
			want:   "📝 Policy Soft Failed",
		},
		{
			name:   "errored status",
			status: "errored",
			want:   "❌ Errored",
		},
		{
			name:   "canceled status",
			status: "canceled",
			want:   "❌ Canceled",
		},
		{
			name:   "discarded status",
			status: "discarded",
			want:   "❌ Discarded",
		},
		{
			name:   "planned_and_finished status",
			status: "planned_and_finished",
			want:   "📝 Planned and Finished",
		},
		{
			name:   "unknown status",
			status: "unknown_status",
			want:   "`unknown_status`",
		},
		{
			name:   "status with backticks",
			status: "`applied`",
			want:   "✅ Applied",
		},
		{
			name:   "status with surrounding backticks",
			status: "`unknown_status`",
			want:   "`unknown_status`",
		},
		{
			name:   "empty status",
			status: "",
			want:   "``",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatStatus(tt.status); got != tt.want {
				t.Errorf("FormatStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}
