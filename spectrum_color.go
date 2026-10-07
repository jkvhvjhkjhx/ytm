package main

import (
	"fmt"
	"math"
	"time"
)

var spectrumColorStart = time.Now()

// Compute one palette per frame so each column keeps the same color across
// all rows. The rainbow travels one full cycle every eighteen seconds.
func spectrumTint() []string {
	palette := [7][3]int{{255, 90, 100}, {255, 160, 70}, {245, 220, 85}, {85, 220, 135}, {60, 225, 235}, {95, 135, 255}, {195, 95, 245}}
	phase := math.Mod(time.Since(spectrumColorStart).Seconds(), 18) / 18
	colors := make([]string, 96)
	for bar := range colors {
		position := math.Mod(phase+float64(bar)/float64(len(colors)), 1) * float64(len(palette))
		i := int(position)
		u := position - float64(i)
		a, b := palette[i], palette[(i+1)%len(palette)]
		var rgb [3]int
		for c := range rgb {
			rgb[c] = int(math.Round(float64(a[c]) + float64(b[c]-a[c])*u))
		}
		colors[bar] = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", rgb[0], rgb[1], rgb[2])
	}
	return colors
}
