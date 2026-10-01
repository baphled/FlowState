package engine

import (
	"errors"
	"testing"

	"github.com/baphled/flowstate/internal/tool"
)

func TestBatchAllFailed(t *testing.T) {
	cases := []struct {
		name    string
		results []tool.Result
		want    bool
	}{
		{name: "empty"},
		{name: "success", results: []tool.Result{{Output: "ok"}}},
		{name: "explicit failure", results: []tool.Result{{IsError: true}}, want: true},
		{name: "error failure", results: []tool.Result{{Error: errors.New("failed")}}, want: true},
		{name: "partial progress", results: []tool.Result{{IsError: true}, {Output: "ok"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := batchAllFailed(tc.results); got != tc.want {
				t.Fatalf("batchAllFailed = %v, want %v", got, tc.want)
			}
		})
	}
}
