package tfc_utils

import (
	"github.com/hashicorp/go-tfe"
	"testing"
)

func Test_shouldScheduleNewRun(t *testing.T) {
	type args struct {
		current *tfe.Run
	}
	tests := []struct {
		name       string
		args       args
		wantOk     bool
		wantCancel bool
	}{
		{
			name: "nil",
			args: args{
				current: nil,
			},
			wantOk:     true,
			wantCancel: false,
		},
		{
			name: "applying MR",
			args: args{
				current: &tfe.Run{
					ID:         "asfasdfdasf",
					Actions:    nil,
					AutoApply:  false,
					HasChanges: true,
					IsDestroy:  false,
					Message:    "Merge request #555",
					Status:     "applying",
				},
			},
			wantOk:     false,
			wantCancel: false,
		},
		{
			name: "scheduled: policy_checked",
			args: args{
				current: &tfe.Run{
					ID:              "asfasdfdasf",
					Actions:         nil,
					AutoApply:       false,
					Message:         RUN_MESSAGE_PREFIX + " asdf",
					PositionInQueue: 0,
					Status:          "policy_checked",
				},
			},
			wantOk:     false,
			wantCancel: false,
		},
		{
			name: "scheduled: policy_checked AutoApply",
			args: args{
				current: &tfe.Run{
					ID:              "asfasdfdasf",
					Actions:         nil,
					AutoApply:       true,
					Message:         RUN_MESSAGE_PREFIX + " asdf",
					PositionInQueue: 0,
					Status:          "policy_checked",
				},
			},
			wantOk:     false,
			wantCancel: false,
		},
		{
			name: "scheduled: policy_override",
			args: args{
				current: &tfe.Run{
					ID:              "asfasdfdasf",
					Actions:         nil,
					AutoApply:       true,
					Message:         RUN_MESSAGE_PREFIX + " asdf",
					PositionInQueue: 0,
					Status:          "policy_override",
				},
			},
			wantOk:     false,
			wantCancel: false,
		},
		{
			name: "scheduled: pending",
			args: args{
				current: &tfe.Run{
					ID:              "asfasdfdasf",
					Actions:         nil,
					AutoApply:       false,
					Message:         RUN_MESSAGE_PREFIX + " asdf",
					PositionInQueue: 0,
					Status:          "pending",
				},
			},
			wantOk:     true,
			wantCancel: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOk, gotCancel := shouldScheduleNewRun(tt.args.current)
			if gotOk != tt.wantOk {
				t.Errorf("shouldScheduleNewRun() gotOk = %v, want %v", gotOk, tt.wantOk)
			}
			if gotCancel != tt.wantCancel {
				t.Errorf("shouldScheduleNewRun() gotCancel = %v, want %v", gotCancel, tt.wantCancel)
			}
		})
	}
}
