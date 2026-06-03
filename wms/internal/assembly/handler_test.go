package assembly

import (
	"net/http"
	"testing"
)

// TestMapServiceError verifies the service-error -> HTTP mapping.
//
// Issue #50 (Assembly-4): ErrOrderNotNew must map to 409 Conflict with code
// "ORDER_NOT_NEW". Before the fix this sentinel had no case in mapServiceError
// and fell through to the default arm (500 INTERNAL_ERROR), masking a legitimate
// business-rule rejection as a server fault. The fix added the explicit case.
//
// mapServiceError is a pure function, so this is a plain table test with no mocks.
func TestMapServiceError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "ErrOrderNotNew maps to 409 ORDER_NOT_NEW",
			err:        ErrOrderNotNew,
			wantStatus: http.StatusConflict,
			wantCode:   "ORDER_NOT_NEW",
		},
		{
			name:       "ErrCartEmpty maps to 409 CART_EMPTY",
			err:        ErrCartEmpty,
			wantStatus: http.StatusConflict,
			wantCode:   "CART_EMPTY",
		},
		{
			// Assembly wrong-buffer guard: scanning another store's shipping buffer
			// while carrying picked goods must surface as a distinct, actionable
			// 409 DESTINATION_MISMATCH — not a misleading CART_EMPTY.
			name:       "ErrDestinationMismatch maps to 409 DESTINATION_MISMATCH",
			err:        ErrDestinationMismatch,
			wantStatus: http.StatusConflict,
			wantCode:   "DESTINATION_MISMATCH",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotCode, _ := mapServiceError(tt.err)
			if gotStatus != tt.wantStatus {
				t.Errorf("mapServiceError(%v) status = %d, want %d", tt.err, gotStatus, tt.wantStatus)
			}
			if gotCode != tt.wantCode {
				t.Errorf("mapServiceError(%v) code = %q, want %q", tt.err, gotCode, tt.wantCode)
			}
		})
	}
}
