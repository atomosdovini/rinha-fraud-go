package index

// int16 quantization with scale 10000 (better precision than int8)
// float in [-1, 1] → int16 in [-10000, 10000]
// sentinel -1.0 → int16(-10000) (preserved exactly)

const (
	scale        = 10000
	VecsPerBlock = 8 // SIMD-friendly: 8 int32s = 1 AVX2 register
)

// QuantizeVec converts a float32[14] vector to int16[14].
// The -1.0 sentinel (indices 5 & 6 for null last_transaction) maps to -10000.
func QuantizeVec(v [Dims]float32) [Dims]int16 {
	var q [Dims]int16
	for i, f := range v {
		q[i] = quantizeScalar(f)
	}
	return q
}

func quantizeScalar(f float32) int16 {
	if f == -1.0 {
		return -scale
	}
	if f < 0 {
		return 0
	}
	if f > 1 {
		return scale
	}
	return int16(f * scale)
}

// QueryInt32 converts a float32 query to int32 for block distance computation.
// int32 avoids overflow when accumulating squared int16 differences.
func QueryInt32(v [Dims]float32) [Dims]int32 {
	var q [Dims]int32
	for i, f := range v {
		q[i] = int32(quantizeScalar(f))
	}
	return q
}
