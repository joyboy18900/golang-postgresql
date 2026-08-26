package service_test

import (
	"strings"
	"testing"

	"golang-postgresql/service"
)

func TestParsePlan(t *testing.T) {
	tests := []struct {
		name       string
		rawJSON    string
		wantErr    bool
		wantSubstr []string
	}{
		{
			name:       "seq scan",
			rawJSON:    `[{"Plan":{"Node Type":"Seq Scan","Relation Name":"audit_log"},"Execution Time":118.4}]`,
			wantSubstr: []string{"Seq Scan on audit_log", "118.40 ms"},
		},
		{
			name: "index scan",
			rawJSON: `[{"Plan":{"Node Type":"Index Scan","Relation Name":"audit_log",` +
				`"Index Name":"idx_audit_log_actor_id_created_at"},"Execution Time":0.6}]`,
			wantSubstr: []string{"Index Scan using idx_audit_log_actor_id_created_at on audit_log", "0.60 ms"},
		},
		{
			name: "parallel seq scan nested under gather",
			rawJSON: `[{"Plan":{"Node Type":"Gather","Plans":[` +
				`{"Node Type":"Parallel Seq Scan","Relation Name":"audit_log"}]},"Execution Time":50.2}]`,
			wantSubstr: []string{"Parallel Seq Scan on audit_log"},
		},
		{
			name: "index scan nested under limit and sort, on a partition",
			rawJSON: `[{"Plan":{"Node Type":"Limit","Plans":[{"Node Type":"Sort","Plans":[` +
				`{"Node Type":"Index Scan","Relation Name":"audit_log_y2026m08",` +
				`"Index Name":"audit_log_y2026m08_actor_id_created_at_idx"}]}]},"Execution Time":0.9}]`,
			wantSubstr: []string{"Index Scan using audit_log_y2026m08_actor_id_created_at_idx on audit_log_y2026m08"},
		},
		{
			name:       "no scan node falls back to root node type",
			rawJSON:    `[{"Plan":{"Node Type":"Result"},"Execution Time":0.01}]`,
			wantSubstr: []string{"Result", "0.01 ms"},
		},
		{
			name:    "malformed json",
			rawJSON: `not json`,
			wantErr: true,
		},
		{
			name:    "empty array",
			rawJSON: `[]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.ParsePlan(tt.rawJSON)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, substr := range tt.wantSubstr {
				if !strings.Contains(result.Summary, substr) {
					t.Errorf("Summary = %q, want substring %q", result.Summary, substr)
				}
			}
		})
	}
}
