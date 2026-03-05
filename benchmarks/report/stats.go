package report

import "math"

// PairedTTestResult holds the result of a paired t-test.
type PairedTTestResult struct {
	MeanDiff float64 `json:"mean_diff"`
	StdErr   float64 `json:"std_err"`
	TStat    float64 `json:"t_stat"`
	PValue   float64 `json:"p_value"`
	N        int     `json:"n"`
}

// PairedTTest performs a paired two-tailed t-test on matched samples.
// Returns the test result. a and b must have equal length.
func PairedTTest(a, b []float64) PairedTTestResult {
	n := len(a)
	if n != len(b) || n < 2 {
		return PairedTTestResult{}
	}

	// Compute differences.
	diffs := make([]float64, n)
	var sumD float64
	for i := range a {
		diffs[i] = a[i] - b[i]
		sumD += diffs[i]
	}
	meanD := sumD / float64(n)

	// Compute standard deviation of differences.
	var sumSq float64
	for _, d := range diffs {
		dev := d - meanD
		sumSq += dev * dev
	}
	sdD := math.Sqrt(sumSq / float64(n-1))

	if sdD == 0 {
		return PairedTTestResult{MeanDiff: meanD, N: n}
	}

	se := sdD / math.Sqrt(float64(n))
	tStat := meanD / se

	// Approximate two-tailed p-value using the t-distribution.
	df := float64(n - 1)
	pValue := 2 * tDistCDF(-math.Abs(tStat), df)

	return PairedTTestResult{
		MeanDiff: meanD,
		StdErr:   se,
		TStat:    tStat,
		PValue:   pValue,
		N:        n,
	}
}

// McNemarResult holds the result of McNemar's test.
type McNemarResult struct {
	B      int     `json:"b"` // a succeeded, b failed
	C      int     `json:"c"` // a failed, b succeeded
	ChiSq  float64 `json:"chi_sq"`
	PValue float64 `json:"p_value"`
}

// McNemarTest performs McNemar's test on paired binary outcomes.
// a[i] and b[i] are true if the task succeeded under condition a/b respectively.
func McNemarTest(a, b []bool) McNemarResult {
	if len(a) != len(b) {
		return McNemarResult{}
	}

	var bCount, cCount int
	for i := range a {
		if a[i] && !b[i] {
			bCount++ // a succeeded, b failed
		}
		if !a[i] && b[i] {
			cCount++ // a failed, b succeeded
		}
	}

	bc := float64(bCount + cCount)
	if bc == 0 {
		return McNemarResult{B: bCount, C: cCount, PValue: 1.0}
	}

	// McNemar's chi-squared statistic with continuity correction.
	diff := math.Abs(float64(bCount)-float64(cCount)) - 1
	if diff < 0 {
		diff = 0
	}
	chiSq := (diff * diff) / bc

	// p-value from chi-squared distribution with 1 df.
	pValue := 1 - chiSquaredCDF(chiSq, 1)

	return McNemarResult{
		B:      bCount,
		C:      cCount,
		ChiSq:  chiSq,
		PValue: pValue,
	}
}

// tDistCDF approximates the CDF of the t-distribution using the
// regularized incomplete beta function.
func tDistCDF(t, df float64) float64 {
	x := df / (df + t*t)
	prob := 0.5 * regBetaInc(df/2, 0.5, x)
	if t > 0 {
		return 1 - prob
	}
	return prob
}

// chiSquaredCDF approximates the CDF of the chi-squared distribution.
func chiSquaredCDF(x, k float64) float64 {
	if x <= 0 {
		return 0
	}
	return regularizedGammaLower(k/2, x/2)
}

// regularizedGammaLower computes the lower regularized incomplete gamma function
// P(a, x) using a series expansion.
func regularizedGammaLower(a, x float64) float64 {
	if x < 0 {
		return 0
	}
	if x == 0 {
		return 0
	}

	lgammaA, _ := math.Lgamma(a)
	sum := 1.0 / a
	term := 1.0 / a
	for n := 1; n < 200; n++ {
		term *= x / (a + float64(n))
		sum += term
		if math.Abs(term) < 1e-15*math.Abs(sum) {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-lgammaA) * sum
}

// regBetaInc computes the regularized incomplete beta function I_x(a, b)
// using a continued fraction expansion (Lentz's method).
func regBetaInc(a, b, x float64) float64 {
	if x < 0 || x > 1 {
		return 0
	}
	if x == 0 || x == 1 {
		return x
	}

	lbeta := lgammaBeta(a, b)
	front := math.Exp(math.Log(x)*a + math.Log(1-x)*b - lbeta)

	// Use continued fraction.
	if x < (a+1)/(a+b+2) {
		return front * betaCF(a, b, x) / a
	}
	return 1 - front*betaCF(b, a, 1-x)/b
}

func betaCF(a, b, x float64) float64 {
	const maxIter = 200
	const eps = 1e-15

	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < eps {
		d = eps
	}
	d = 1 / d
	h := d

	for m := 1; m <= maxIter; m++ {
		fm := float64(m)
		// Even step.
		num := fm * (b - fm) * x / ((qam + 2*fm) * (a + 2*fm))
		d = 1 + num*d
		if math.Abs(d) < eps {
			d = eps
		}
		c = 1 + num/c
		if math.Abs(c) < eps {
			c = eps
		}
		d = 1 / d
		h *= d * c

		// Odd step.
		num = -(a + fm) * (qab + fm) * x / ((a + 2*fm) * (qap + 2*fm))
		d = 1 + num*d
		if math.Abs(d) < eps {
			d = eps
		}
		c = 1 + num/c
		if math.Abs(c) < eps {
			c = eps
		}
		d = 1 / d
		delta := d * c
		h *= delta

		if math.Abs(delta-1) < eps {
			break
		}
	}
	return h
}

func lgammaBeta(a, b float64) float64 {
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	return la + lb - lab
}
