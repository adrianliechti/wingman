package provider

import "math"

// CosineSimilarity returns the cosine similarity of two equal-length vectors,
// in [-1, 1]. It returns 0 when the lengths differ or either vector is zero.
func CosineSimilarity(a []float32, b []float32) float32 {
	if len(a) != len(b) {
		return 0.0
	}

	dotproduct := 0.0

	magnitudeA := 0.0
	magnitudeB := 0.0

	for k := range a {
		valA := float64(a[k])
		valB := float64(b[k])

		dotproduct += valA * valB

		magnitudeA += valA * valA
		magnitudeB += valB * valB
	}

	if magnitudeA == 0 || magnitudeB == 0 {
		return 0.0
	}

	return float32(dotproduct / (math.Sqrt(magnitudeA) * math.Sqrt(magnitudeB)))
}
