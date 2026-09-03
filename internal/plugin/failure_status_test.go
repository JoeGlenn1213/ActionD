// Copyright (c) 2025 JoeGlenn1213
// ActionD Plugin - failure-status mapping tests (ASSURANCE Phase C incident)

package plugin

import "testing"

// TestIsFailureResult locks the failure vocabulary. The V1 ActionResult
// wrappers (plugins/*/run.py to_action_result) emit status "failed" on test
// failure; before this test existed the runner only recognized
// "error"/"failure" and let "failed" pass as DONE (false PASS in
// production, caught by the Handoff Gate fixture on 2026-08-22).
func TestIsFailureResult(t *testing.T) {
	cases := map[string]bool{
		"error":   true,
		"failure": true,
		"failed":  true,
		"success": false,
		"warning": false,
		"pass":    false,
		"":        false,
		"weird":   false,
	}
	for status, want := range cases {
		if got := isFailureResult(&StructuredResult{Status: status}); got != want {
			t.Errorf("isFailureResult(%q) = %v, want %v", status, got, want)
		}
	}
	if isFailureResult(nil) {
		t.Error("nil result must not be a failure")
	}
}
