package main

// Presets are deliberately the only user-facing visualizer setting.
type barPreset struct {
	ID, Name               string
	Bottom, Top            [3]int
	Attack, Decay, Spatial float64
	Glyph                  rune
}

var barPresets = []barPreset{
	{"classic", "Classic Bars", [3]int{112, 214, 232}, [3]int{112, 214, 232}, .035, .22, 0, '█'},
	{"glass", "Glass Bars", [3]int{88, 228, 236}, [3]int{124, 177, 213}, .035, .24, .08, '█'},
	{"soft_fade", "Soft Fade Bars", [3]int{152, 218, 233}, [3]int{63, 90, 111}, .065, .36, .23, '│'},
	{"neon", "Neon Bars", [3]int{83, 245, 224}, [3]int{106, 163, 221}, .03, .23, .12, '┃'},
	{"mist", "Mist Bars", [3]int{180, 209, 214}, [3]int{91, 115, 127}, .075, .42, .27, '│'},
}

func presetFor(id string) barPreset {
	for _, p := range barPresets {
		if p.ID == id {
			return p
		}
	}
	return barPresets[1]
}
