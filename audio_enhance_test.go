package main

import (
	"strings"
	"testing"
)

func TestMPVAudioArgs(t *testing.T) {
	defer setAudioSettings(true, "none")
	setAudioSettings(false, "none")
	if a := mpvAudioArgs(); a != nil {
		t.Fatalf("expected no args, got %v", a)
	}
	setAudioSettings(true, "bogus")
	a := strings.Join(mpvAudioArgs(), " ")
	if !strings.Contains(a, "dynaudnorm") || strings.Contains(a, "equalizer") {
		t.Fatalf("unexpected args %q", a)
	}
	setAudioSettings(true, "bass")
	if a = strings.Join(mpvAudioArgs(), " "); !strings.Contains(a, "equalizer=f=80") {
		t.Fatalf("missing eq: %q", a)
	}
}

func TestChu2PresetsYieldChain(t *testing.T) {
	for _, k := range []string{"chu2", "chu2-kazi", "chu2-full"} {
		setAudioSettings(false, k)
		args := mpvAudioArgs()
		if len(args) == 0 || !strings.Contains(args[len(args)-1], "volume=-") {
			t.Fatalf("%s: bad chain %v", k, args)
		}
	}
	setAudioSettings(true, "none")
}

func TestAudioCompressChain(t *testing.T) {
	defer func() { setAudioCompress(false); setAudioSettings(true, "none") }()
	setAudioSettings(false, "none")
	setAudioCompress(false)
	if a := mpvAudioArgs(); a != nil {
		t.Fatalf("expected no args, got %v", a)
	}
	setAudioCompress(true)
	a := strings.Join(mpvAudioArgs(), " ")
	if !strings.Contains(a, "acompressor") || !strings.Contains(a, "alimiter") || strings.Contains(a, "dynaudnorm") {
		t.Fatalf("compress alone: %q", a)
	}
	setAudioSettings(true, "chu2-full")
	a = strings.Join(mpvAudioArgs(), " ")
	if strings.Index(a, "volume=-4dB") > strings.Index(a, "acompressor") || !strings.Contains(a, "dynaudnorm") {
		t.Fatalf("order: %q", a)
	}
}

func TestAudioOptionArgs(t *testing.T) {
	defer func() { setAudioOptions(false, false, 1); setAudioCompress(false); setAudioSettings(true, "none") }()
	setAudioSettings(false, "none")
	setAudioCompress(false)
	setAudioOptions(false, false, 1)
	if a := mpvAudioArgs(); a != nil {
		t.Fatalf("expected nil, got %v", a)
	}
	setAudioOptions(false, true, 1)
	a := strings.Join(mpvAudioArgs(), " ")
	if !strings.Contains(a, "--audio-exclusive=yes") || strings.Contains(a, "--af") {
		t.Fatalf("exclusive: %q", a)
	}
	setAudioOptions(true, false, 1)
	if a = strings.Join(mpvAudioArgs(), " "); !strings.Contains(a, "resampler=soxr,precision=28") {
		t.Fatalf("resample: %q", a)
	}
	if normalizeAudioWidth(0.5) != 1 || normalizeAudioWidth(9) != 2.5 || normalizeAudioWidth(1.2) != 1.2 {
		t.Fatal("width clamp")
	}
	setAudioOptions(false, false, 1.2)
	setAudioCompress(true)
	setAudioSettings(true, "chu2-full")
	a = strings.Join(mpvAudioArgs(), " ")
	if strings.Count(a, "alimiter") != 1 || !strings.HasSuffix(a, "alimiter=limit=0.95]") ||
		strings.Index(a, "extrastereo=m=1.2") > strings.Index(a, "acompressor") || strings.Index(a, "highshelf") > strings.Index(a, "extrastereo") {
		t.Fatalf("chain: %q", a)
	}
	setAudioCompress(false)
	a = strings.Join(mpvAudioArgs(), " ")
	if strings.Count(a, "alimiter") != 1 || !strings.Contains(a, "extrastereo") {
		t.Fatalf("width only: %q", a)
	}
}

func TestAudioGain(t *testing.T) {
	defer func() {
		setAudioGain(0)
		setAudioCompress(false)
		setAudioOptions(false, false, 1)
		setAudioSettings(true, "none")
	}()
	if normalizeAudioGain(30) != 12 || normalizeAudioGain(-30) != -12 || normalizeAudioGain(4) != 4 {
		t.Fatal("gain clamp")
	}
	setAudioSettings(false, "none")
	setAudioCompress(false)
	setAudioOptions(false, false, 1)
	setAudioGain(0)
	if a := mpvAudioArgs(); a != nil {
		t.Fatalf("expected nil, got %v", a)
	}
	setAudioGain(4)
	a := strings.Join(mpvAudioArgs(), " ")
	if !strings.Contains(a, "--af=lavfi=[volume=4dB,alimiter=limit=0.95]") {
		t.Fatalf("gain alone: %q", a)
	}
	setAudioGain(-3)
	if a = strings.Join(mpvAudioArgs(), " "); !strings.Contains(a, "volume=-3dB") || strings.Contains(a, "alimiter") {
		t.Fatalf("negative gain: %q", a)
	}
	setAudioGain(4)
	setAudioCompress(true)
	setAudioOptions(false, false, 1.2)
	setAudioSettings(true, "chu2-full")
	a = strings.Join(mpvAudioArgs(), " ")
	i, j, k := strings.Index(a, "extrastereo"), strings.Index(a, "volume=4dB"), strings.Index(a, "acompressor")
	if !(i < j && j < k) || strings.Count(a, "alimiter") != 1 || !strings.HasSuffix(a, "alimiter=limit=0.95]") {
		t.Fatalf("chain: %q", a)
	}
}
