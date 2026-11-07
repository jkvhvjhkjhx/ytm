package main

import (
	"math"
	"strconv"
	"sync"
)

// Audio enhancement settings (config keys audio_enhance, audio_eq). They are
// applied when mpv starts, so changes take effect on the next ytm launch.
var (
	audioMu        sync.RWMutex
	audioEnhance   = true
	audioEQ        = "none"
	audioCompress  = false
	audioResample  = false
	audioExclusive = false
	audioWidth     = 1.0
	audioGain      = 0.0
)

// audioEQFilters maps an audio_eq preset to mpv/lavfi equalizer filters.
var audioEQFilters = map[string]string{
	"bass":  "equalizer=f=80:t=q:w=1:g=3,equalizer=f=200:t=q:w=1:g=1",
	"vivid": "equalizer=f=80:t=q:w=1:g=2,equalizer=f=3000:t=q:w=1:g=1.5,equalizer=f=10000:t=q:w=1:g=2",
	"vocal": "equalizer=f=250:t=q:w=1:g=-2,equalizer=f=500:t=q:w=1:g=-1.5,equalizer=f=1000:t=q:w=1:g=2.5,equalizer=f=2500:t=q:w=1:g=4,equalizer=f=4000:t=q:w=1:g=3,equalizer=f=6000:t=q:w=1:g=1.5",
	// Moondrop Chu 2 IEM AutoEq presets; replace other presets, do not stack.
	"chu2":      "volume=-2.5dB,lowshelf=f=105:t=q:w=0.7:g=-4.1,equalizer=f=173:t=q:w=0.65:g=-3.7,equalizer=f=773:t=q:w=0.54:g=2.2,equalizer=f=6178:t=q:w=3.65:g=2.7,equalizer=f=69:t=q:w=0.75:g=2.4,highshelf=f=10000:t=q:w=0.7:g=-2.4,equalizer=f=2276:t=q:w=2.51:g=0.8,equalizer=f=1315:t=q:w=2.48:g=-0.7,equalizer=f=865:t=q:w=2.45:g=0.4,equalizer=f=3830:t=q:w=2.83:g=-0.4",
	"chu2-kazi": "volume=-2.5dB,lowshelf=f=105:t=q:w=0.7:g=-3.4,equalizer=f=163:t=q:w=0.72:g=-2.9,equalizer=f=720:t=q:w=0.83:g=2.0,equalizer=f=5976:t=q:w=2.12:g=2.5,equalizer=f=70:t=q:w=0.92:g=2.6,highshelf=f=10000:t=q:w=0.7:g=-1.6,equalizer=f=3092:t=q:w=3.10:g=-1.1,equalizer=f=2109:t=q:w=2.85:g=0.9,equalizer=f=8586:t=q:w=5.11:g=1.5,equalizer=f=1249:t=q:w=3.47:g=-0.3",
	"chu2-full": "volume=-4dB,lowshelf=f=105:t=q:w=0.7:g=1.5,equalizer=f=69:t=q:w=0.75:g=2.4,equalizer=f=173:t=q:w=0.65:g=-0.5,equalizer=f=773:t=q:w=0.54:g=2.2,equalizer=f=865:t=q:w=2.45:g=0.4,equalizer=f=1315:t=q:w=2.48:g=-0.7,equalizer=f=2276:t=q:w=2.51:g=0.8,equalizer=f=3830:t=q:w=2.83:g=-0.4,equalizer=f=6178:t=q:w=3.65:g=2.7,highshelf=f=10000:t=q:w=0.7:g=-1.0",
}

func normalizeAudioEQ(v string) string {
	if _, ok := audioEQFilters[v]; ok {
		return v
	}
	return "none"
}

// Gentle compressor (audio_compress); a peak limiter always ends the chain when
// audio_compress is on or audio_width > 1.
const (
	audioCompressFilter = "acompressor=threshold=-20dB:ratio=2:attack=30:release=250:knee=4:makeup=2dB"
	audioLimiterFilter  = "alimiter=limit=0.95"
)

// normalizeAudioWidth clamps audio_width to 1.0..2.5 (1.0 = off).
func normalizeAudioWidth(w float64) float64 {
	if math.IsNaN(w) || w < 1 {
		return 1
	}
	return math.Min(w, 2.5)
}

// normalizeAudioGain clamps audio_gain (dB) to -12..+12 (0 = off).
func normalizeAudioGain(g float64) float64 {
	if math.IsNaN(g) {
		return 0
	}
	return math.Max(-12, math.Min(12, g))
}

func setAudioGain(g float64) {
	audioMu.Lock()
	audioGain = normalizeAudioGain(g)
	audioMu.Unlock()
}

// setAudioOptions sets audio_resample, audio_exclusive and audio_width.
func setAudioOptions(resample, exclusive bool, width float64) {
	audioMu.Lock()
	audioResample, audioExclusive, audioWidth = resample, exclusive, normalizeAudioWidth(width)
	audioMu.Unlock()
}

func setAudioCompress(on bool) {
	audioMu.Lock()
	audioCompress = on
	audioMu.Unlock()
}

func setAudioSettings(enhance bool, eq string) {
	audioMu.Lock()
	audioEnhance, audioEQ = enhance, normalizeAudioEQ(eq)
	audioMu.Unlock()
}

// mpvAudioArgs returns extra mpv arguments for the current audio settings.
func mpvAudioArgs() []string {
	audioMu.RLock()
	enhance, eq, compress := audioEnhance, audioEQ, audioCompress
	resample, exclusive, width, gain := audioResample, audioExclusive, audioWidth, audioGain
	audioMu.RUnlock()
	if !enhance && !compress && !resample && !exclusive && width <= 1 && gain == 0 && eq == "none" {
		return nil
	}
	var chain []string
	if f := audioEQFilters[eq]; f != "" {
		chain = append(chain, f)
	}
	if width > 1 {
		chain = append(chain, "extrastereo=m="+strconv.FormatFloat(width, 'f', -1, 64))
	}
	if gain != 0 {
		chain = append(chain, "volume="+strconv.FormatFloat(gain, 'f', -1, 64)+"dB")
	}
	if compress {
		chain = append(chain, audioCompressFilter)
	}
	if enhance {
		chain = append(chain, "dynaudnorm=f=250:g=15:p=0.9")
	}
	if compress || width > 1 || gain > 0 {
		chain = append(chain, audioLimiterFilter)
	}
	args := []string{"--audio-samplerate=48000", "--audio-channels=stereo"}
	if resample {
		args = append(args, "--audio-swresample-o=resampler=soxr,precision=28")
	}
	if exclusive {
		args = append(args, "--audio-exclusive=yes")
	}
	if len(chain) == 0 {
		return args
	}
	joined := ""
	for i, c := range chain {
		if i > 0 {
			joined += ","
		}
		joined += c
	}
	return append(args, "--af=lavfi=["+joined+"]")
}
