//go:build windows

package main

import (
	"math"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CAVA's existing stream is device loopback, not process loopback. This guard
// conservatively suppresses it when another active audio session is detected.
// It is NOT process-isolated capture: session notifications/polling can lag.
// We make this limitation explicit in the tray and MINI-PLAYER.md.
type miniCOM struct{ table *[40]uintptr }

func (p *miniCOM) call(index int, args ...uintptr) uintptr {
	all := append([]uintptr{uintptr(unsafe.Pointer(p))}, args...)
	r, _, _ := syscall.SyscallN(p.table[index], all...)
	return r
}
func (p *miniCOM) release() {
	if p != nil {
		p.call(2)
	}
}
func miniGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

var miniOLE = windows.NewLazySystemDLL("ole32.dll")
var miniEnumeratorClass = miniGUID("{BCDE0395-E52F-467C-8E3D-C4579291692E}")
var miniEnumeratorIID = miniGUID("{A95664D2-9614-4F35-A746-DE8DB63617E6}")
var miniSessionManagerIID = miniGUID("{77AA99A0-1BD6-484F-8BC7-2C654C9A9B6F}")
var miniSession2IID = miniGUID("{BFB7FF88-7239-4FC9-8FA2-07C950BE9C6D}")
var miniMeterIID = miniGUID("{C02216F6-8C67-4B5B-9D00-D008E73E0064}")
var miniVolumeIID = miniGUID("{87CE5498-68D6-44E5-9215-6DA47EF883D8}")

type miniAudioGuard struct {
	enumerator  *miniCOM
	initialized bool
	endpoint    string
	conflictPID uint32
	lastMixed   time.Time
}

func (g *miniAudioGuard) init() {
	r, _, _ := miniOLE.NewProc("CoInitializeEx").Call(0, 0)
	if int32(r) < 0 {
		return
	}
	g.initialized = true
	r, _, _ = miniOLE.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&miniEnumeratorClass)), 0, 1, uintptr(unsafe.Pointer(&miniEnumeratorIID)), uintptr(unsafe.Pointer(&g.enumerator)))
	if int32(r) < 0 {
		g.enumerator = nil
		return
	}
	if device := g.defaultDevice(0); device != nil {
		g.endpoint = miniDeviceID(device)
		device.release()
	}
}
func (g *miniAudioGuard) close() {
	g.enumerator.release()
	g.enumerator = nil
	if g.initialized {
		miniOLE.NewProc("CoUninitialize").Call()
		g.initialized = false
	}
}
func (g *miniAudioGuard) defaultDevice(role uintptr) *miniCOM {
	if g.enumerator == nil {
		return nil
	}
	var d *miniCOM
	if int32(g.enumerator.call(4, 0, role, uintptr(unsafe.Pointer(&d)))) < 0 {
		return nil
	}
	return d
}
func miniDeviceID(d *miniCOM) string {
	var id *uint16
	if int32(d.call(5, uintptr(unsafe.Pointer(&id)))) < 0 || id == nil {
		return ""
	}
	defer miniOLE.NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(id)))
	return windows.UTF16PtrToString(id)
}
func (g *miniAudioGuard) allow(pid, capturePID uint32) (bool, string) {
	g.conflictPID = 0
	if pid == 0 || g.endpoint == "" {
		return false, "Audio source unverified"
	}
	d := g.defaultDevice(0)
	if d == nil {
		return false, "Audio device unavailable"
	}
	defer d.release()
	if miniDeviceID(d) != g.endpoint {
		return false, "Output changed: restart YTM spectrum"
	}
	otherRole := g.defaultDevice(1)
	if otherRole == nil {
		return false, "Audio source unverified"
	}
	roleID := miniDeviceID(otherRole)
	otherRole.release()
	if roleID != g.endpoint {
		return false, "Default audio roles differ"
	}
	var manager, enumeration *miniCOM
	if int32(d.call(3, uintptr(unsafe.Pointer(&miniSessionManagerIID)), 1, 0, uintptr(unsafe.Pointer(&manager)))) < 0 || manager == nil {
		return false, "Audio sessions unavailable"
	}
	defer manager.release()
	if int32(manager.call(5, uintptr(unsafe.Pointer(&enumeration)))) < 0 || enumeration == nil {
		return false, "Audio sessions unavailable"
	}
	defer enumeration.release()
	var count int32
	if int32(enumeration.call(3, uintptr(unsafe.Pointer(&count)))) < 0 || count < 0 || count > 1024 {
		return false, "Audio sessions unavailable"
	}
	matched := false
	for i := int32(0); i < count; i++ {
		var session *miniCOM
		if int32(enumeration.call(4, uintptr(i), uintptr(unsafe.Pointer(&session)))) < 0 || session == nil {
			return false, "Audio source unverified"
		}
		active, sessionPID, valid := miniSessionIdentity(session)
		if !valid {
			session.release()
			return false, "Audio source unverified"
		}
		if !active {
			session.release()
			continue
		}
		// WASAPI also enumerates our own loopback capture client as active.
		// CAVA only captures; it does not contribute sound to the output mix.
		if capturePID != 0 && sessionPID == capturePID {
			session.release()
			continue
		}
		if sessionPID != pid {
			audible, known := miniSessionAudible(session)
			session.release()
			if !known {
				return false, "Audio source unverified"
			}
			if !audible {
				continue
			}
			g.conflictPID = sessionPID
			g.lastMixed = time.Now()
			return false, "Spectrum suspended: shared audio output"
		}
		session.release()
		matched = true
	}
	if !matched {
		return false, "Waiting for this mpv audio session"
	}
	if time.Since(g.lastMixed) < 500*time.Millisecond {
		return false, "Spectrum suspended: shared audio tail"
	}
	return true, "Device loopback · mpv session verified"
}

// An active browser AudioContext can be silent. Meter only foreign sessions;
// mute/zero volume cannot contribute to the mix. Unknown meters stay blocked.
func miniSessionAudible(session *miniCOM) (bool, bool) {
	var volume *miniCOM
	if int32(session.call(0, uintptr(unsafe.Pointer(&miniVolumeIID)), uintptr(unsafe.Pointer(&volume)))) < 0 || volume == nil {
		return false, false
	}
	var muted uint32
	var level float32
	r1 := volume.call(6, uintptr(unsafe.Pointer(&muted)))
	r2 := volume.call(4, uintptr(unsafe.Pointer(&level)))
	volume.release()
	if int32(r1) < 0 || int32(r2) < 0 {
		return false, false
	}
	if muted != 0 || level <= 0 {
		return false, true
	}
	var meter *miniCOM
	if int32(session.call(0, uintptr(unsafe.Pointer(&miniMeterIID)), uintptr(unsafe.Pointer(&meter)))) < 0 || meter == nil {
		return false, false
	}
	defer meter.release()
	var peak float32
	if int32(meter.call(3, uintptr(unsafe.Pointer(&peak)))) < 0 || math.IsNaN(float64(peak)) || peak < 0 || peak > 1 {
		return false, false
	}
	return peak > .00002, true
}
func miniSessionIdentity(session *miniCOM) (active bool, pid uint32, valid bool) {
	var state uint32
	if int32(session.call(3, uintptr(unsafe.Pointer(&state)))) < 0 {
		return
	}
	if state != 1 {
		return false, 0, true
	}
	var control *miniCOM
	if int32(session.call(0, uintptr(unsafe.Pointer(&miniSession2IID)), uintptr(unsafe.Pointer(&control)))) < 0 || control == nil {
		return true, 0, false
	}
	defer control.release()
	// S_OK only: AUDCLNT_S_NO_SINGLE_PROCESS is deliberately rejected.
	if control.call(14, uintptr(unsafe.Pointer(&pid))) != 0 {
		return true, 0, false
	}
	return true, pid, true
}
