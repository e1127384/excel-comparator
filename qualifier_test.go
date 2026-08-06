package main

import (
	"fmt"
	"testing"
)

func TestQualifier(t *testing.T) {
	tests := []struct {
		expr string
		row  map[string]string
		want bool
	}{
		{"Status != Cancelled", map[string]string{"Status": "Open"}, true},
		{"Status != Cancelled", map[string]string{"Status": "Cancelled"}, false},
		{"Case Status != Cancelled", map[string]string{"Case Status": "Open"}, true},
		{"Case Id NOT IN (id1, id2)", map[string]string{"Case Id": "id3"}, true},
		{"Case Id NOT IN (id1, id2)", map[string]string{"Case Id": "id1"}, false},
		{"Status != Cancelled AND Case Id NOT IN (id1, id2)", map[string]string{"Status": "Open", "Case Id": "id3"}, true},
		{"Status != Cancelled AND Case Id NOT IN (id1, id2)", map[string]string{"Status": "Cancelled", "Case Id": "id3"}, false},
		{"Status != Cancelled AND Case Id NOT IN (id1, id2)", map[string]string{"Status": "Open", "Case Id": "id1"}, false},
		{"(Status = Open OR Status = Pending) AND Priority != Low",
			map[string]string{"Status": "Open", "Priority": "High"}, true},
		{"(Status = Open OR Status = Pending) AND Priority != Low",
			map[string]string{"Status": "Closed", "Priority": "High"}, false},
		// case-insensitive
		{"Status != cancelled", map[string]string{"Status": "Cancelled"}, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s", tt.expr), func(t *testing.T) {
			expr, err := parseQualifierExpr(tt.expr)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			got := expr.Evaluate(tt.row, false)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
