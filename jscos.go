package main

// Kosinus wie in V8 (Node): fdlibm-Algorithmus (__kernel_cos, __kernel_sin, Reduktion auf π/2). Go-Bibliothek und V8 unterscheiden sich
// in der letzten Stelle; damit würden Vereinfachung und Richtungen an einzelnen Stellen anders gerundet.

import "math"

const (
	fdInvpio2 = 6.36619772367581382433e-01
	fdPio2_1  = 1.57079632673412561417e+00
	fdPio2_1t = 6.07710050650619224932e-11
	fdS1      = -1.66666666666666324348e-01
	fdS2      = 8.33333333332248946124e-03
	fdS3      = -1.98412698298579493134e-04
	fdS4      = 2.75573137070700676789e-06
	fdS5      = -2.50507602534068634195e-08
	fdS6      = 1.58969099521155010221e-10
	fdC1      = 4.16666666666666019037e-02
	fdC2      = -1.38888888888741095749e-03
	fdC3      = 2.48015872894767294178e-05
	fdC4      = -2.75573143513906633035e-07
	fdC5      = 2.08757232129817482790e-09
	fdC6      = -1.13596475577881948265e-11
)

// fdKernelCos wie __kernel_cos(x, y)
func fdKernelCos(x, y float64) float64 {
	ix := int32(math.Float64bits(x)>>32) & 0x7fffffff
	if ix < 0x3e400000 {
		if int(x) == 0 {
			return 1
		}
	}
	z := x * x
	r := z * (fdC1 + z*(fdC2+z*(fdC3+z*(fdC4+z*(fdC5+z*fdC6)))))
	if ix < 0x3FD33333 {
		return 1 - (0.5*z - (z*r - x*y))
	}
	var qx float64
	if ix > 0x3fe90000 {
		qx = 0.28125
	} else {
		qx = math.Float64frombits(uint64(uint32(ix-0x00200000)) << 32)
	}
	hz := 0.5*z - qx
	a := 1 - qx
	return a - (hz - (z*r - x*y))
}

// fdKernelSin wie __kernel_sin(x, y, iy)
func fdKernelSin(x, y float64, iy int) float64 {
	z := x * x
	v := z * x
	r := fdS2 + z*(fdS3+z*(fdS4+z*(fdS5+z*fdS6)))
	if iy == 0 {
		return x + v*(fdS1+z*r)
	}
	return x - ((z*(0.5*y-v*r) - y) - v*fdS1)
}

// fdRemPio2 reduziert x auf y0+y1 mit n (Vielfaches von π/2); für die Größen, die hier vorkommen (|x| < 2^19·π/2)
func fdRemPio2(x float64) (int, float64, float64) {
	fn := math.RoundToEven(x * fdInvpio2)
	n := int(fn)
	r := x - fn*fdPio2_1
	w := fn * fdPio2_1t
	y0 := r - w
	return n, y0, (r - y0) - w
}

// jsCos entspricht Math.cos in V8
func jsCos(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return math.NaN()
	}
	ix := int32(math.Float64bits(x)>>32) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return fdKernelCos(x, 0)
	}
	n, y0, y1 := fdRemPio2(x)
	switch n & 3 {
	case 0:
		return fdKernelCos(y0, y1)
	case 1:
		return -fdKernelSin(y0, y1, 1)
	case 2:
		return -fdKernelCos(y0, y1)
	default:
		return fdKernelSin(y0, y1, 1)
	}
}
