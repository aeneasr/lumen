package report

import (
	"math"
	"testing"
)

func TestPairedTTest_SignificantDifference(t *testing.T) {
	// Clear difference: a is consistently higher than b.
	a := []float64{10, 12, 11, 13, 14, 10, 12, 11}
	b := []float64{5, 6, 5, 7, 6, 5, 6, 5}

	result := PairedTTest(a, b)

	if result.N != 8 {
		t.Errorf("N: got %d, want 8", result.N)
	}
	if result.MeanDiff <= 0 {
		t.Errorf("MeanDiff should be positive, got %f", result.MeanDiff)
	}
	if result.PValue >= 0.05 {
		t.Errorf("expected significant p-value < 0.05, got %f", result.PValue)
	}
}

func TestPairedTTest_NoDifference(t *testing.T) {
	a := []float64{10, 10, 10, 10}
	b := []float64{10, 10, 10, 10}

	result := PairedTTest(a, b)

	if result.MeanDiff != 0 {
		t.Errorf("MeanDiff: got %f, want 0", result.MeanDiff)
	}
}

func TestPairedTTest_TooFewSamples(t *testing.T) {
	result := PairedTTest([]float64{1}, []float64{2})
	if result.N != 0 {
		t.Errorf("expected empty result for n<2, got N=%d", result.N)
	}
}

func TestPairedTTest_UnequalLength(t *testing.T) {
	result := PairedTTest([]float64{1, 2, 3}, []float64{1, 2})
	if result.N != 0 {
		t.Errorf("expected empty result for unequal lengths")
	}
}

func TestMcNemarTest_SignificantDifference(t *testing.T) {
	// 10 tasks where a always succeeds and b always fails.
	a := make([]bool, 10)
	b := make([]bool, 10)
	for i := range a {
		a[i] = true
		b[i] = false
	}

	result := McNemarTest(a, b)

	if result.B != 10 {
		t.Errorf("B (a success, b fail): got %d, want 10", result.B)
	}
	if result.C != 0 {
		t.Errorf("C (a fail, b success): got %d, want 0", result.C)
	}
	if result.PValue >= 0.05 {
		t.Errorf("expected significant p-value, got %f", result.PValue)
	}
}

func TestMcNemarTest_NoDifference(t *testing.T) {
	a := []bool{true, false, true, false}
	b := []bool{true, false, true, false}

	result := McNemarTest(a, b)
	if result.PValue != 1.0 {
		t.Errorf("expected p=1.0 for identical outcomes, got %f", result.PValue)
	}
}

func TestMcNemarTest_UnequalLength(t *testing.T) {
	result := McNemarTest([]bool{true}, []bool{true, false})
	if result.B != 0 && result.C != 0 {
		t.Errorf("expected empty result for unequal lengths")
	}
}

func TestRegularizedGammaLower(t *testing.T) {
	// P(1, 1) = 1 - e^-1 ≈ 0.6321
	got := regularizedGammaLower(1, 1)
	want := 1 - math.Exp(-1)
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("regularizedGammaLower(1,1): got %f, want %f", got, want)
	}
}
