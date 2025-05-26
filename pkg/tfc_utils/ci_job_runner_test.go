package tfc_utils

import (
	"testing"

	"github.com/hashicorp/go-tfe"
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
		{
			name: "scheduled: planning (can cancel)",
			args: args{
				current: &tfe.Run{
					ID:      "run-planning",
					Message: RUN_MESSAGE_PREFIX + " test",
					Status:  "planning",
				},
			},
			wantOk:     true,
			wantCancel: true,
		},
		{
			name: "scheduled: applying (can cancel)",
			args: args{
				current: &tfe.Run{
					ID:      "run-applying",
					Message: RUN_MESSAGE_PREFIX + " test",
					Status:  "applying",
				},
			},
			wantOk:     true,
			wantCancel: true,
		},
		{
			name: "scheduled: planned (can cancel)",
			args: args{
				current: &tfe.Run{
					ID:      "run-planned",
					Message: RUN_MESSAGE_PREFIX + " test",
					Status:  "planned",
				},
			},
			wantOk:     true,
			wantCancel: true,
		},
		{
			name: "finished run - should schedule new",
			args: args{
				current: &tfe.Run{
					ID:      "run-finished",
					Message: "Manual run",
					Status:  "applied",
				},
			},
			wantOk:     true,
			wantCancel: false,
		},
		{
			name: "manual run in progress - should not schedule",
			args: args{
				current: &tfe.Run{
					ID:      "run-manual",
					Message: "Manual run from UI",
					Status:  "planning",
				},
			},
			wantOk:     false,
			wantCancel: false,
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

func Test_isUnfinishedRun(t *testing.T) {
	tests := []struct {
		name string
		run  *tfe.Run
		want bool
	}{
		{
			name: "nil run",
			run:  nil,
			want: false,
		},
		{
			name: "apply_queued status",
			run:  &tfe.Run{Status: "apply_queued"},
			want: true,
		},
		{
			name: "applying status",
			run:  &tfe.Run{Status: "applying"},
			want: true,
		},
		{
			name: "confirmed status",
			run:  &tfe.Run{Status: "confirmed"},
			want: true,
		},
		{
			name: "cost_estimated status",
			run:  &tfe.Run{Status: "cost_estimated"},
			want: true,
		},
		{
			name: "cost_estimating status",
			run:  &tfe.Run{Status: "cost_estimating"},
			want: true,
		},
		{
			name: "plan_queued status",
			run:  &tfe.Run{Status: "plan_queued"},
			want: true,
		},
		{
			name: "policy_checked status",
			run:  &tfe.Run{Status: "policy_checked"},
			want: true,
		},
		{
			name: "policy_checking status",
			run:  &tfe.Run{Status: "policy_checking"},
			want: true,
		},
		{
			name: "policy_soft_failed status",
			run:  &tfe.Run{Status: "policy_soft_failed"},
			want: true,
		},
		{
			name: "policy_override status",
			run:  &tfe.Run{Status: "policy_override"},
			want: true,
		},
		{
			name: "planned status",
			run:  &tfe.Run{Status: "planned"},
			want: true,
		},
		{
			name: "planning status",
			run:  &tfe.Run{Status: "planning"},
			want: true,
		},
		{
			name: "pending status",
			run:  &tfe.Run{Status: "pending"},
			want: true,
		},
		{
			name: "applied status (finished)",
			run:  &tfe.Run{Status: "applied"},
			want: false,
		},
		{
			name: "discarded status (finished)",
			run:  &tfe.Run{Status: "discarded"},
			want: false,
		},
		{
			name: "errored status (finished)",
			run:  &tfe.Run{Status: "errored"},
			want: false,
		},
		{
			name: "canceled status (finished)",
			run:  &tfe.Run{Status: "canceled"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUnfinishedRun(tt.run); got != tt.want {
				t.Errorf("isUnfinishedRun() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_canCancelRun(t *testing.T) {
	tests := []struct {
		name string
		run  *tfe.Run
		want bool
	}{
		{
			name: "nil run",
			run:  nil,
			want: false,
		},
		{
			name: "policy_checked status (cannot cancel)",
			run:  &tfe.Run{Status: "policy_checked"},
			want: false,
		},
		{
			name: "policy_override status (cannot cancel)",
			run:  &tfe.Run{Status: "policy_override"},
			want: false,
		},
		{
			name: "pending status (can cancel)",
			run:  &tfe.Run{Status: "pending"},
			want: true,
		},
		{
			name: "planning status (can cancel)",
			run:  &tfe.Run{Status: "planning"},
			want: true,
		},
		{
			name: "planned status (can cancel)",
			run:  &tfe.Run{Status: "planned"},
			want: true,
		},
		{
			name: "confirmed status (can cancel)",
			run:  &tfe.Run{Status: "confirmed"},
			want: true,
		},
		{
			name: "applying status (can cancel)",
			run:  &tfe.Run{Status: "applying"},
			want: true,
		},
		{
			name: "policy_checking status (can cancel)",
			run:  &tfe.Run{Status: "policy_checking"},
			want: true,
		},
		{
			name: "policy_soft_failed status (can cancel)",
			run:  &tfe.Run{Status: "policy_soft_failed"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canCancelRun(tt.run); got != tt.want {
				t.Errorf("canCancelRun() = %v, want %v", got, tt.want)
			}
		})
	}
}
