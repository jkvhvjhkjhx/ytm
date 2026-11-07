package main

import "sync"

// Audio enhancement settings (config keys audio_enhance, audio_eq). They are
// applied when mpv starts, so changes take effect on the next ytm launch.
var (
	audioMu      sync.RWMutex
	audioEnhance = true
	audioEQ      = "none"
)

// audioEQFilters maps an audio_eq preset to mpv/lavfi equalizer filters.
var audioEQFilters = map[string]string{
	"bass":  "equalizer=f=80:t=q:w=1:g=3,equalizer=f=200:t=q:w=1:g=1",
	"vivid": "equalizer=f=80:t=q:w=1:g=2,equalizer=f=3000:t=q:w=1:g=1.5,equalizer=f=10000:t=q:w=1:g=2",
}

func normalizeAudioEQ(v string) string {
	if _, ok := audioEQFilters[v]; ok {
		return v
	}
	return "none"
}

func setAudioSettings(enhance bool, eq string) {
	audioMu.Lock()
	audioEnhance, audioEQ = enhance, normalizeAudioEQ(eq)
	audioMu.Unlock()
}

// mpvAudioArgs returns extra mpv arguments for the current audio settings.
func mpvAudioArgs() []string {
	audioMu.RLock()
	enhance, eq := audioEnhance, audioEQ
	audioMu.RUnlock()
	if !enhance && eq == "none" {
		return nil
	}
	var chain []string
	if f := audioEQFilters[eq]; f != "" {
		chain = append(chain, f)
	}
	if enhance {
		chain = append(chain, "dynaudnorm=f=250:g=15:p=0.9")
	}
	args := []string{"--audio-samplerate=48000", "--audio-channels=stereo"}
	joined := ""
	for i, c := range chain {
		if i > 0 {
			joined += ","
		}
		joined += c
	}
	return append(args, "--af=lavfi=["+joined+"]")
}
