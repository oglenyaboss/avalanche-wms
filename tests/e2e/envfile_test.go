//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestWithValidJWTSecret covers the .env JWT_SECRET substitution that keeps the e2e
// stack hermetic. Run without the Docker stack via:
//
//	E2E_SKIP_TESTMAIN=true go test -tags=e2e -run=TestWithValidJWTSecret ./...
func TestWithValidJWTSecret(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantLine   string
		wantChange bool
	}{
		{
			name:       "blocklisted placeholder is replaced",
			in:         "DB_USER=root\nJWT_SECRET=replace-with-a-random-64-character-secret\nFOO=bar",
			wantLine:   "JWT_SECRET=" + e2eTestJWTSecret,
			wantChange: true,
		},
		{
			name:       "empty value is replaced",
			in:         "JWT_SECRET=",
			wantLine:   "JWT_SECRET=" + e2eTestJWTSecret,
			wantChange: true,
		},
		{
			name:       "real developer secret is preserved",
			in:         "JWT_SECRET=a-genuinely-random-and-sufficiently-long-secret",
			wantLine:   "JWT_SECRET=a-genuinely-random-and-sufficiently-long-secret",
			wantChange: false,
		},
		{
			name:       "missing line is appended",
			in:         "DB_USER=root\nDB_PASSWORD=root",
			wantLine:   "JWT_SECRET=" + e2eTestJWTSecret,
			wantChange: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := withValidJWTSecret(tc.in)
			if changed != tc.wantChange {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChange)
			}
			if !strings.Contains(got, tc.wantLine) {
				t.Fatalf("result %q does not contain %q", got, tc.wantLine)
			}
		})
	}
}
