package main

import (
	"image"
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestMiniConfigBackwardsCompatible(t *testing.T) {
	c := defaultConfig()
	if _, err := toml.Decode("volume = 40\nvisualizer_fps = 24", &c); err != nil {
		t.Fatal(err)
	}
	if c.MiniPlayer != defaultMiniConfig() {
		t.Fatal("old config disabled companion defaults")
	}
	if _, err := toml.Decode("[mini_player]\nenabled = false\nthumbnail = false\nalways_visible = true\ncolor = 'theme'", &c); err != nil {
		t.Fatal(err)
	}
	if c.MiniPlayer.Enabled || c.MiniPlayer.Thumbnail || !c.MiniPlayer.AlwaysVisible || c.MiniPlayer.Color != "theme" {
		t.Fatal("explicit false/settings ignored")
	}
}

func TestMiniGeometryAndContainment(t *testing.T) {
	for _, dpi := range []int{96, 120, 144, 192, 288, 480} {
		for _, width := range []int{0, 40, 60, 96, 160, 200, 400, 1000} {
			for _, bands := range []int{0, 1, 4, 20, 96} {
				g := miniGeometry(dpi, width, defaultMiniConfig(), bands)
				if g.W < 0 || g.W > width || g.Count > bands || g.Count > 32 {
					t.Fatalf("invalid %+v", g)
				}
				for i := 0; i < g.Count; i++ {
					x := g.Start + i*(g.Bar+g.Gap)
					if x < g.SpecX || x+g.Bar > g.W || g.Gap < 2 {
						t.Fatalf("bar overflow %+v", g)
					}
				}
			}
		}
	}
	g := miniGeometry(96, 1920, defaultMiniConfig(), 96)
	if g.W != 200 || g.H != 44 || g.Count < 20 || g.Bar < 2 || g.Bar > 3 {
		t.Fatalf("default cramped %+v", g)
	}
	for _, work := range []miniRect{{0, 0, 1920, 1040}, {-1920, 0, 0, 1040}, {0, 48, 1920, 1080}, {48, 0, 1920, 1080}} {
		x, y := miniPosition(miniRect{work.Left, 0, work.Right, 1080}, work, 200, 44, 8)
		if x < int(work.Left) || x+200 > int(work.Right) || y < int(work.Top) || y+44 > int(work.Bottom) {
			t.Fatal("placement outside work area")
		}
	}
}

func TestMiniVisibility(t *testing.T) {
	c := defaultMiniConfig()
	if miniShouldShow(c, true, false, false, false, false) {
		t.Fatal("shown over restored terminal")
	}
	if !miniShouldShow(c, true, true, false, false, false) || !miniShouldShow(c, true, false, true, false, false) {
		t.Fatal("not shown minimized/hidden")
	}
	if !miniShouldShow(c, true, true, false, true, false) || !miniShouldShow(c, true, false, true, true, false) {
		t.Fatal("fullscreen hid minimized / hidden companion")
	}
	if miniShouldShow(c, true, false, false, true, false) {
		t.Fatal("fullscreen bypassed restored-host visibility setting")
	}
	c.AlwaysVisible = true
	if !miniShouldShow(c, true, false, false, true, false) {
		t.Fatal("Always Visible hidden in fullscreen")
	}
	if miniShouldShow(c, true, true, false, true, true) {
		t.Fatal("shown on another desktop")
	}
	c.Enabled = false
	if miniShouldShow(c, true, true, true, false, false) {
		t.Fatal("disabled overlay visible")
	}
}

func TestMiniDynamicsRealBandDistribution(t *testing.T) {
	now := time.Unix(1000, 0)
	in := miniInput{Active: true, Count: 96, Received: now}
	for i := 8; i < 14; i++ {
		in.Bands[i] = .6
	}
	for i := 70; i < 80; i++ {
		in.Bands[i] = .15
	}
	var s miniSignal
	for i := 0; i < 10; i++ {
		s.step(in, 28, now.Add(time.Duration(i)*time.Second/30), true)
	}
	if s.Levels[3] < .35 || s.Levels[21] <= 0 || s.Levels[3] < s.Levels[21]*1.8 || s.Levels[13] > .001 {
		t.Fatal("frequency distribution flattened or invented", s.Levels)
	}
	in.Active = false
	for i := 10; i < 100; i++ {
		s.step(in, 28, now.Add(time.Duration(i)*time.Second/30), true)
	}
	for _, v := range s.Levels {
		if v != 0 {
			t.Fatal("pause did not settle", v)
		}
	}
	in.Active = true
	in.Received = now.Add(4 * time.Second)
	s.step(in, 28, in.Received, false)
	for _, v := range s.Levels {
		if v != 0 {
			t.Fatal("unverified audio animated")
		}
	}
}

func TestMiniRateIndependenceAndSilence(t *testing.T) {
	start := time.Unix(1000, 0)
	var outputs [2]miniSignal
	for j, rate := range []int{30, 60} {
		in := miniInput{Active: true, Count: 96}
		for i := range in.Bands {
			in.Bands[i] = .25
		}
		s := &outputs[j]
		s.Last = start
		s.Gain = 1
		for i := 1; i <= rate; i++ {
			now := start.Add(time.Duration(float64(time.Second) * float64(i) / float64(rate)))
			in.Received = now
			s.step(in, 28, now, true)
		}
	}
	if math.Abs(outputs[0].Levels[0]-outputs[1].Levels[0]) > .015 {
		t.Fatal("frame-dependent dynamics")
	}
	s := miniSignal{}
	in := miniInput{Active: true, Count: 96, Received: start}
	s.step(in, 28, start, true)
	for _, v := range s.Levels {
		if v != 0 {
			t.Fatal("fake silence activity")
		}
	}
}

func TestMiniPixelsAlphaArtworkAndGaps(t *testing.T) {
	in := miniInput{Settings: defaultMiniConfig(), Art: image.NewUniform(color.NRGBA{255, 0, 0, 255})}
	// Finite 16:9 image: contain must leave top/bottom letterbox visible.
	art := image.NewNRGBA(image.Rect(0, 0, 160, 90))
	for y := 0; y < 90; y++ {
		for x := 0; x < 160; x++ {
			art.SetNRGBA(x, y, color.NRGBA{255, 0, 0, 255})
		}
	}
	in.Art = art
	g := miniGeometry(96, 1920, in.Settings, 96)
	var r miniSurface
	r.prepare(g, in, false)
	s := miniSignal{}
	for i := 0; i < g.Count; i++ {
		s.Levels[i] = .75
	}
	r.paint(&s)
	if r.Pixels[3] != 0 {
		t.Fatal("opaque square corner")
	}
	if r.Pixels[(g.ArtY*g.W+g.ArtX+18)*4+2] > 100 {
		t.Fatal("art stretched vertically")
	}
	if r.Pixels[((g.ArtY+18)*g.W+g.ArtX+18)*4+2] < 240 {
		t.Fatal("art not visible")
	}
	row := g.SpecY + g.SpecH - 2
	for i := 0; i < g.Count-1; i++ {
		x := g.Start + i*(g.Bar+g.Gap)
		if r.Pixels[(row*g.W+x)*4+1] < 150 {
			t.Fatal("bar too faint")
		}
		for k := g.Bar; k < g.Bar+g.Gap; k++ {
			at := (row*g.W + x + k) * 4
			if r.Pixels[at] != r.Base[at] || r.Pixels[at+3] != r.Base[at+3] {
				t.Fatal("gap painted")
			}
		}
	}
	for i := 0; i < len(r.Pixels); i += 4 {
		for k := 0; k < 3; k++ {
			if r.Pixels[i+k] > r.Pixels[i+3] {
				t.Fatal("non-premultiplied alpha")
			}
		}
	}
}

func TestMiniMailboxAndCommands(t *testing.T) {
	p := &miniPlayer{actions: make(chan miniActionMsg, 1)}
	p.publish(miniInput{Generation: 1})
	p.pushBands([]float64{.8, .3}, time.Now())
	p.publish(miniInput{Generation: 2})
	if p.read().Count != 0 {
		t.Fatal("old track spectrum retained")
	}
	p.action(miniNext)
	p.action(miniPrevious) // bounded, never blocks playback
	if <-p.actions != miniNext {
		t.Fatal("wrong command")
	}
	m := newModel(defaultConfig(), nil, "", "")
	m.inputMode = true
	m.helpOpen = true
	// Transport actions bypass the search editor rather than injecting keys.
	m.handleMiniAction(miniPause)
	if !m.inputMode || !m.helpOpen {
		t.Fatal("tray modified editor focus")
	}
}

func BenchmarkMiniFrame(b *testing.B) {
	in := miniInput{Settings: defaultMiniConfig(), Active: true, Count: 96, Received: time.Now()}
	for i := range in.Bands {
		in.Bands[i] = .4
	}
	g := miniGeometry(144, 1920, in.Settings, 96)
	var r miniSurface
	r.prepare(g, in, false)
	var s miniSignal
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.step(in, g.Count, in.Received, true)
		r.paint(&s)
	}
}

func TestMiniDragThresholdAndCoordinates(t *testing.T) {
	var d miniDrag
	d.begin(miniPoint{100, 200}, miniPoint{-500, 600}, 4, 4)
	if _, _, moved := d.move(miniPoint{102, 199}); moved {
		t.Fatal("click jitter became a drag")
	}
	x, y, moved := d.move(miniPoint{90, 215})
	if !moved || x != -510 || y != 615 {
		t.Fatalf("bad drag: %d,%d moved=%v", x, y, moved)
	}
	if _, _, moved := d.move(miniPoint{100, 200}); !moved {
		t.Fatal("returning to start would accidentally restore terminal")
	}
	d.pressed = false
	if _, _, moved := d.move(miniPoint{120, 220}); moved {
		t.Fatal("movement after release")
	}
	d.begin(miniPoint{}, miniPoint{}, 4, 4)
	if d.moved {
		t.Fatal("new click retained previous drag")
	}
}

func TestMiniDragWorkAreaClamp(t *testing.T) {
	for _, tc := range []struct {
		work                     miniRect
		x, y, w, h, wantX, wantY int
	}{
		{miniRect{0, 0, 1920, 1040}, 400, 500, 200, 44, 400, 500},
		{miniRect{0, 0, 1920, 1040}, 5000, 5000, 200, 44, 1720, 996},
		{miniRect{-1920, -1080, 0, -40}, -5000, -5000, 200, 44, -1920, -1080},
		{miniRect{-1920, -1080, 0, -40}, 50, 50, 200, 44, -200, -84},
		{miniRect{0, 0, 100, 20}, 50, 50, 200, 44, 0, 0},
	} {
		x, y := miniClampPosition(tc.work, tc.x, tc.y, tc.w, tc.h)
		if x != tc.wantX || y != tc.wantY {
			t.Fatalf("%+v: got %d,%d", tc, x, y)
		}
	}
}

func TestMiniFullscreenVersusMaximizedBrowser(t *testing.T) {
	monitor := miniRect{0, 0, 1920, 1080}
	for _, tc := range []struct {
		name  string
		frame miniRect
		style uintptr
		want  bool
	}{
		{"maximized browser resize borders", miniRect{-8, -8, 1928, 1088}, 0x00cf0000, false},
		{"maximized browser auto-hide taskbar", monitor, 0x00cf0000, false},
		{"ordinary browser", miniRect{100, 100, 1500, 900}, 0x00cf0000, false},
		{"F11 or fullscreen video", monitor, 0x90000000, true},
		{"borderless small window", miniRect{100, 100, 1500, 900}, 0x90000000, false},
		{"fullscreen other monitor", miniRect{-1920, 0, 0, 1080}, 0x90000000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := miniFrameFullscreen(monitor, tc.frame, tc.style); got != tc.want {
				t.Fatalf("fullscreen=%v, want %v", got, tc.want)
			}
		})
	}
	for state := uint32(0); state <= 7; state++ {
		if got := miniExclusivePresentation(state); got != (state == 3 || state == 4) {
			t.Fatalf("wrong notification-state policy for %d", state)
		}
	}
}
