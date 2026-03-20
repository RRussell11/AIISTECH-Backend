package site_test

import (
	"testing"

	"github.com/RRussell11/AIISTECH-Backend/internal/site"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		siteID  string
		wantErr bool
	}{
		{name: "valid simple", siteID: "local", wantErr: false},
		{name: "valid with dash", siteID: "my-site", wantErr: false},
		{name: "valid with underscore", siteID: "site_01", wantErr: false},
		{name: "empty", siteID: "", wantErr: true},
		{name: "dot dot", siteID: "..", wantErr: true},
		{name: "contains dot dot", siteID: "abc..def", wantErr: true},
		{name: "forward slash", siteID: "a/b", wantErr: true},
		{name: "backslash", siteID: `a\b`, wantErr: true},
		{name: "only slash", siteID: "/", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := site.Validate(tc.siteID)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate(%q) error = %v, wantErr = %v", tc.siteID, err, tc.wantErr)
			}
		})
	}
}
