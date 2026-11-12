package model

import (
	"math"
	"testing"
)

func TestAILimitsUnlimitedAndPrecision(t *testing.T) {
	for _, quota := range []float64{0, 0.00000001, 1, 1000000} {
		c := AIPlanSettings{GroupID: 1, Quota: quota, Rate5H: 1}
		if err := c.ValidateLimits(); err != nil {
			t.Fatalf("valid quota %g: %v", quota, err)
		}
	}
	for _, quota := range []float64{-1, 0.000000001, 1000001, math.NaN(), math.Inf(1)} {
		if (AIPlanSettings{GroupID: 1, Quota: quota}).ValidateLimits() == nil {
			t.Fatalf("unsafe quota %g accepted", quota)
		}
	}
	if (AIPlanSettings{Quota: 0}).ValidateLimits() == nil {
		t.Fatal("unlimited quota bypassed group validation")
	}
	if (AIPlanSettings{GroupID: 1, Rate1D: -1}).ValidateLimits() == nil {
		t.Fatal("invalid periodic limit accepted")
	}
}
