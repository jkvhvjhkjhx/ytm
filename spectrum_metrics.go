package main

import "math"

// Distribution contains RMS in eight consecutive logarithmic CAVA band groups
// from low to high (50 Hz to 16 kHz), not calibrated acoustic power/dBFS.
type spectrumMetrics struct {
	Bands         int        `json:"bands"`
	Peak          float64    `json:"peak"`
	RMS           float64    `json:"rms"`
	ActivePercent float64    `json:"active_percent"`
	Distribution  [8]float64 `json:"low_to_high_rms"`
}

func measureSpectrum(src []float64) spectrumMetrics {
	m := spectrumMetrics{Bands: len(src)}
	if len(src) == 0 {
		return m
	}
	sum, active := 0., 0
	var count [8]int
	for i, v := range src {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			v = 0
		}
		v = math.Max(0, math.Min(1, v))
		sum += v * v
		m.Peak = math.Max(m.Peak, v)
		if v > .015 {
			active++
		}
		group := min(7, i*8/len(src))
		m.Distribution[group] += v * v
		count[group]++
	}
	m.RMS = math.Sqrt(sum / float64(len(src)))
	m.ActivePercent = 100 * float64(active) / float64(len(src))
	for i := range m.Distribution {
		if count[i] > 0 {
			m.Distribution[i] = math.Sqrt(m.Distribution[i] / float64(count[i]))
		}
	}
	return m
}
