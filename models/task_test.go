package models

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		in      TaskInput
		wantErr bool
		status  string
	}{
		{"ok with default status", TaskInput{Title: "  hello "}, false, StatusTodo},
		{"ok with status", TaskInput{Title: "a", Status: StatusDone}, false, StatusDone},
		{"empty title", TaskInput{Title: "   "}, true, ""},
		{"long title", TaskInput{Title: strings.Repeat("x", 201)}, true, ""},
		{"bad status", TaskInput{Title: "a", Status: "later"}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			err := in.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && in.Status != tc.status {
				t.Fatalf("status = %q, want %q", in.Status, tc.status)
			}
		})
	}
}
