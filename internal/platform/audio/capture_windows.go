//go:build windows

package audio

import (
	"aioverlay/internal/domain"
	"context"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/windows"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var ole = windows.NewLazySystemDLL("ole32.dll")
var coCreate = ole.NewProc("CoCreateInstance")
var coInit = ole.NewProc("CoInitializeEx")
var coUninit = ole.NewProc("CoUninitialize")
var coFree = ole.NewProc("CoTaskMemFree")
var clsEnumerator = windows.GUID{Data1: 0xbcde0395, Data2: 0xe52f, Data3: 0x467c, Data4: [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
var iidEnumerator = windows.GUID{Data1: 0xa95664d2, Data2: 0x9614, Data3: 0x4f35, Data4: [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
var iidClient = windows.GUID{Data1: 0x1cb9ad4c, Data2: 0xdbfa, Data3: 0x4c32, Data4: [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
var iidCapture = windows.GUID{Data1: 0xc8adbd64, Data2: 0xe71e, Data3: 0x48a0, Data4: [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}

func call(obj unsafe.Pointer, index int, args ...uintptr) uintptr {
	vt := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vt, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	a := append([]uintptr{uintptr(obj)}, args...)
	hr, _, _ := syscall.SyscallN(fn, a...)
	return hr
}
func release(p unsafe.Pointer) {
	if p != nil {
		call(p, 2)
	}
}
func result(name string, hr uintptr) error {
	if int32(hr) < 0 {
		code := uint32(hr)
		return &CaptureError{Operation: name, HRESULT: code, Kind: classifyWASAPIHRESULT(code)}
	}
	return nil
}
func enumerator() (unsafe.Pointer, error) {
	var p unsafe.Pointer
	hr, _, _ := coCreate.Call(uintptr(unsafe.Pointer(&clsEnumerator)), 0, 23, uintptr(unsafe.Pointer(&iidEnumerator)), uintptr(unsafe.Pointer(&p)))
	if err := result("enumerator", hr); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("WASAPI enumerator unavailable")
	}
	return p, nil
}
func apartment() (func(), error) {
	runtime.LockOSThread()
	hr, _, _ := coInit.Call(0, 2)
	if err := result("COM STA", hr); err != nil {
		runtime.UnlockOSThread()
		return nil, err
	}
	return func() { coUninit.Call(); runtime.UnlockOSThread() }, nil
}
func deviceID(p unsafe.Pointer) (string, error) {
	var text *uint16
	if e := result("device ID", call(p, 5, uintptr(unsafe.Pointer(&text)))); e != nil {
		return "", e
	}
	if text == nil {
		return "", fmt.Errorf("WASAPI device ID missing")
	}
	defer coFree.Call(uintptr(unsafe.Pointer(text)))
	return windows.UTF16PtrToString(text), nil
}
func deviceName(p unsafe.Pointer) string {
	var store unsafe.Pointer
	if result("properties", call(p, 4, 0, uintptr(unsafe.Pointer(&store)))) != nil || store == nil {
		return "Audio device"
	}
	defer release(store)
	key := struct {
		ID  windows.GUID
		PID uint32
	}{windows.GUID{Data1: 0xa45c254e, Data2: 0xdf1c, Data3: 0x4efd, Data4: [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, 14}
	var variant [24]byte
	if result("friendly name", call(store, 5, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&variant[0])))) != nil {
		return "Audio device"
	}
	defer ole.NewProc("PropVariantClear").Call(uintptr(unsafe.Pointer(&variant[0])))
	if binary.LittleEndian.Uint16(variant[:]) != 31 {
		return "Audio device"
	}
	ptr := *(**uint16)(unsafe.Pointer(&variant[8]))
	if ptr == nil {
		return "Audio device"
	}
	return windows.UTF16PtrToString(ptr)
}
func Devices() ([]Device, error) {
	done, err := apartment()
	if err != nil {
		return nil, err
	}
	defer done()
	en, err := enumerator()
	if err != nil {
		return nil, err
	}
	defer release(en)
	out := []Device{}
	for flow, mode := range []string{"system", "microphone"} {
		var collection unsafe.Pointer
		if err := result("list endpoints", call(en, 3, uintptr(flow), 1, uintptr(unsafe.Pointer(&collection)))); err != nil {
			return nil, err
		}
		if collection == nil {
			continue
		}
		var count uint32
		err = result("endpoint count", call(collection, 3, uintptr(unsafe.Pointer(&count))))
		if err != nil {
			release(collection)
			return nil, err
		}
		for i := uint32(0); i < count && i < 128; i++ {
			var d unsafe.Pointer
			if result("endpoint", call(collection, 4, uintptr(i), uintptr(unsafe.Pointer(&d)))) != nil || d == nil {
				continue
			}
			id, e := deviceID(d)
			if e == nil {
				out = append(out, Device{ID: id, Name: deviceName(d), Mode: mode})
			}
			release(d)
		}
		release(collection)
	}
	return out, nil
}

// Capture exclusively owns all COM interfaces on this STA. Shared-mode loopback
// duplicates the render stream; it never writes to/mutes/reconfigures playback.
func Capture(ctx context.Context, cfg domain.AudioSourceConfig, emit func(Block)) error {
	if cfg.Mode != "system" && cfg.Mode != "microphone" {
		return fmt.Errorf("unsupported audio source")
	}
	done, err := apartment()
	if err != nil {
		return err
	}
	defer done()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	en, err := enumerator()
	if err != nil {
		return err
	}
	defer release(en)
	var device unsafe.Pointer
	if cfg.DeviceID == "" {
		flow := uintptr(0)
		if cfg.Mode == "microphone" {
			flow = 1
		}
		role := uintptr(1) // eMultimedia
		if cfg.DeviceRole == "communications" {
			role = 2
		}
		err = result("default endpoint", call(en, 4, flow, role, uintptr(unsafe.Pointer(&device))))
	} else {
		id, e := windows.UTF16PtrFromString(cfg.DeviceID)
		if e != nil {
			return fmt.Errorf("invalid audio device ID")
		}
		err = result("selected endpoint", call(en, 5, uintptr(unsafe.Pointer(id)), uintptr(unsafe.Pointer(&device))))
	}
	if err != nil {
		return err
	}
	if device == nil {
		return fmt.Errorf("audio device not available")
	}
	defer release(device)
	var client unsafe.Pointer
	if err = result("activate", call(device, 3, uintptr(unsafe.Pointer(&iidClient)), 23, 0, uintptr(unsafe.Pointer(&client)))); err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("audio client not available")
	}
	defer release(client)
	var format *byte
	if err = result("mix format", call(client, 8, uintptr(unsafe.Pointer(&format)))); err != nil {
		return err
	}
	if format == nil {
		return fmt.Errorf("empty audio format")
	}
	defer coFree.Call(uintptr(unsafe.Pointer(format)))
	head := unsafe.Slice(format, 18)
	size := 18 + int(binary.LittleEndian.Uint16(head[16:]))
	if size > 512 {
		return fmt.Errorf("audio format too large")
	}
	f, err := ParseFormat(unsafe.Slice(format, size))
	if err != nil {
		return err
	}
	flags := uintptr(0)
	if cfg.Mode == "system" {
		flags = 0x20000
	}
	if err = result("initialize shared capture", call(client, 3, 0, flags, 1000000, 0, uintptr(unsafe.Pointer(format)), 0)); err != nil {
		return err
	}
	var capture unsafe.Pointer
	if err = result("capture service", call(client, 14, uintptr(unsafe.Pointer(&iidCapture)), uintptr(unsafe.Pointer(&capture)))); err != nil {
		return err
	}
	if capture == nil {
		return fmt.Errorf("audio capture service unavailable")
	}
	defer release(capture)
	if err = result("start", call(client, 10)); err != nil {
		return err
	}
	defer call(client, 11)
	// Polling avoids pre-1703 event-driven loopback limitations. Buffers are released
	// before conversion/callback work, and every HRESULT is checked.
	emit(Block{Rate: f.Rate, At: time.Now()})
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	lastPacket := time.Now()
	var captureClock captureClock
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			for {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var frames uint32
				if err = result("packet size", call(capture, 5, uintptr(unsafe.Pointer(&frames)))); err != nil {
					return err
				}
				if frames == 0 {
					break
				}
				var data *byte
				var count, flag uint32
				var position, qpc uint64
				if err = result("get buffer", call(capture, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&flag)), uintptr(unsafe.Pointer(&position)), uintptr(unsafe.Pointer(&qpc)))); err != nil {
					return err
				}
				if count == 0 {
					break
				}
				var raw []byte
				if count > uint32(f.Rate*2) {
					call(capture, 4, uintptr(count))
					return fmt.Errorf("audio packet exceeds safe allocation")
				}
				flags := decodeWASAPIBufferFlags(flag)
				if !flags.Silent {
					if data == nil {
						call(capture, 4, uintptr(count))
						return fmt.Errorf("audio packet pointer missing")
					}
					raw = append([]byte(nil), unsafe.Slice(data, int(count)*f.Align)...)
				}
				if err = result("release buffer", call(capture, 4, uintptr(count))); err != nil {
					return err
				}
				samples, e := MonoPCM(raw, int(count), f, flags.Silent)
				if e != nil {
					return e
				}
				lastPacket = now
				emit(Block{
					Samples:        samples,
					Rate:           f.Rate,
					At:             captureClock.PacketStart(qpc, int(count), f.Rate, now, flags.TimestampError),
					Silent:         flags.Silent,
					Discontinuity:  flags.Discontinuity,
					TimestampError: flags.TimestampError,
				})
			}
			if now.Sub(lastPacket) > 100*time.Millisecond {
				emit(Block{Rate: f.Rate, At: now})
				lastPacket = now
			}
		}
	}
}
