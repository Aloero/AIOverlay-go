//go:build windows

package audio

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"aioverlay/internal/domain"
	"golang.org/x/sys/windows"
)

const (
	virtualAudioDeviceProcessLoopback = `VAD\Process_Loopback`
	vtBlob                            = 65
	processLoopbackActivation         = 1
	processLoopbackIncludeTree        = 0

	audclntStreamflagsLoopback       = 0x00020000
	audclntStreamflagsEventCallback  = 0x00040000
	audclntStreamflagsAutoconvertPCM = 0x80000000
)

var (
	mmdevapi                    = windows.NewLazySystemDLL("mmdevapi.dll")
	activateAudioInterfaceAsync = mmdevapi.NewProc("ActivateAudioInterfaceAsync")
	coAlloc                     = ole.NewProc("CoTaskMemAlloc")
	ntdll                       = windows.NewLazySystemDLL("ntdll.dll")
	rtlMoveMemory               = ntdll.NewProc("RtlMoveMemory")
	iidActivateCompletion       = windows.GUID{Data1: 0x41d949ab, Data2: 0x9862, Data3: 0x444a, Data4: [8]byte{0x80, 0xf6, 0xc2, 0x61, 0x33, 0x4d, 0xa5, 0xeb}}
	iidIUnknown                 = windows.GUID{Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidAgileObject              = windows.GUID{Data1: 0x94ea2b94, Data2: 0xe9cc, Data3: 0x49e0, Data4: [8]byte{0xc0, 0xff, 0xee, 0x64, 0xca, 0x8f, 0x5b, 0x90}}
)

func GetCapabilities() Capabilities {
	info := windows.RtlGetVersion()
	capabilities := Capabilities{
		SystemLoopback:              true,
		Microphone:                  true,
		MinimumProcessLoopbackBuild: minimumProcessLoopbackBuild,
	}
	if info.BuildNumber < minimumProcessLoopbackBuild {
		capabilities.ProcessLoopbackReason = fmt.Sprintf("Захват приложения требует Windows build %d или новее; текущий build %d", minimumProcessLoopbackBuild, info.BuildNumber)
		return capabilities
	}
	if err := activateAudioInterfaceAsync.Find(); err != nil {
		capabilities.ProcessLoopbackReason = "ActivateAudioInterfaceAsync недоступен в mmdevapi.dll"
		return capabilities
	}
	capabilities.ProcessLoopback = true
	return capabilities
}

func Processes() ([]Process, error) {
	capabilities := GetCapabilities()
	if !capabilities.ProcessLoopback {
		return nil, &ProcessCaptureError{Kind: ProcessCaptureUnsupported, Message: capabilities.ProcessLoopbackReason}
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("list audio process candidates: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("first process candidate: %w", err)
	}
	processes := make([]Process, 0, 128)
	for len(processes) < 512 {
		if process, ok := inspectProcess(entry.ProcessID, entry.ParentProcessID); ok {
			processes = append(processes, process)
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	sort.Slice(processes, func(i, j int) bool {
		left, right := strings.ToLower(processes[i].Name), strings.ToLower(processes[j].Name)
		if left != right {
			return left < right
		}
		if processes[i].Path != processes[j].Path {
			return strings.ToLower(processes[i].Path) < strings.ToLower(processes[j].Path)
		}
		return processes[i].PID < processes[j].PID
	})
	return processes, nil
}

func inspectProcess(pid, parentPID uint32) (Process, bool) {
	if pid == 0 {
		return Process{}, false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Process{}, false
	}
	defer windows.CloseHandle(handle)
	return inspectProcessHandle(handle, pid, parentPID)
}

func inspectProcessHandle(handle windows.Handle, pid, parentPID uint32) (Process, bool) {
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil || size == 0 {
		return Process{}, false
	}
	path := windows.UTF16ToString(buffer[:size])
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return Process{}, false
	}
	name := path
	if index := strings.LastIndexAny(path, `\/`); index >= 0 && index+1 < len(path) {
		name = path[index+1:]
	}
	return Process{PID: pid, ParentPID: parentPID, Name: name, Path: path, StartedAt: time.Unix(0, creation.Nanoseconds())}, true
}

func CheckProcessTarget(config *domain.AudioProcessConfig) error {
	_, err := resolveConfiguredProcess(config)
	return err
}

func resolveConfiguredProcess(config *domain.AudioProcessConfig) (processIdentity, error) {
	if config == nil {
		return processIdentity{}, &ProcessCaptureError{Kind: ProcessCaptureUnavailable, Message: "process-only capture has no process selection"}
	}
	processes, err := Processes()
	if err != nil {
		return processIdentity{}, err
	}
	return selectProcessIdentity(config.ExecutablePath, processes)
}

func holdProcessIdentity(identity processIdentity) (windows.Handle, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, identity.PID)
	if err != nil {
		return 0, &ProcessCaptureError{Kind: ProcessCaptureUnavailable, Message: "process-only target stopped before capture activation"}
	}
	process, ok := inspectProcessHandle(handle, identity.PID, 0)
	current := processIdentity{PID: process.PID, Path: process.Path, StartedAt: process.StartedAt}
	if !ok || !sameProcessIdentity(identity, current) {
		windows.CloseHandle(handle)
		return 0, &ProcessCaptureError{Kind: ProcessCaptureChanged, Message: "process-only target identity changed before capture activation"}
	}
	return handle, nil
}

func ensureProcessAlive(handle windows.Handle) error {
	wait, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return fmt.Errorf("check process-only target lifetime: %w", err)
	}
	switch wait {
	case uint32(windows.WAIT_TIMEOUT):
		return nil
	case 0:
		return &ProcessCaptureError{Kind: ProcessCaptureChanged, Message: "process-only target exited; capture stopped before a reused PID could redirect audio"}
	default:
		return fmt.Errorf("unexpected process lifetime wait result %d", wait)
	}
}

type audioClientActivationParams struct {
	ActivationType uint32
	TargetPID      uint32
	LoopbackMode   uint32
}

type activationResult struct {
	client uintptr
	err    error
}

type activateCompletionHandler struct {
	refs   atomic.Uint32
	done   chan activationResult
	object uintptr
	vtable uintptr
}

var (
	activationHandlers            sync.Map
	activateQueryInterfaceAddress = syscall.NewCallback(activateQueryInterface)
	activateAddRefAddress         = syscall.NewCallback(activateAddRef)
	activateReleaseAddress        = syscall.NewCallback(activateRelease)
	activateCompletedAddress      = syscall.NewCallback(activateCompleted)
)

func newActivateCompletionHandler() (*activateCompletionHandler, error) {
	vtable, err := allocateNative(4 * unsafe.Sizeof(uintptr(0)))
	if err != nil {
		return nil, err
	}
	callbacks := [...]uintptr{activateQueryInterfaceAddress, activateAddRefAddress, activateReleaseAddress, activateCompletedAddress}
	copyToNative(vtable, unsafe.Pointer(&callbacks[0]), unsafe.Sizeof(callbacks))
	object, err := allocateNative(unsafe.Sizeof(uintptr(0)))
	if err != nil {
		coFree.Call(vtable)
		return nil, err
	}
	copyToNative(object, unsafe.Pointer(&vtable), unsafe.Sizeof(vtable))
	handler := &activateCompletionHandler{done: make(chan activationResult, 1), object: object, vtable: vtable}
	handler.refs.Store(1)
	activationHandlers.Store(object, handler)
	return handler, nil
}

func (handler *activateCompletionHandler) free() {
	if handler == nil {
		return
	}
	activationHandlers.Delete(handler.object)
	if handler.object != 0 {
		coFree.Call(handler.object)
		handler.object = 0
	}
	if handler.vtable != 0 {
		coFree.Call(handler.vtable)
		handler.vtable = 0
	}
}

func activationHandler(this uintptr) (*activateCompletionHandler, bool) {
	value, ok := activationHandlers.Load(this)
	if !ok {
		return nil, false
	}
	handler, ok := value.(*activateCompletionHandler)
	return handler, ok
}

func activateQueryInterface(this, iidPtr, objectPtr uintptr) uintptr {
	if this == 0 || iidPtr == 0 || objectPtr == 0 {
		return uintptr(uint32(0x80004003)) // E_POINTER
	}
	handler, ok := activationHandler(this)
	if !ok {
		return uintptr(uint32(0x80004003))
	}
	var iid windows.GUID
	copyFromNative(unsafe.Pointer(&iid), iidPtr, unsafe.Sizeof(iid))
	if iid != iidIUnknown && iid != iidActivateCompletion && iid != iidAgileObject {
		writeNativeUintptr(objectPtr, 0)
		return uintptr(uint32(0x80004002)) // E_NOINTERFACE
	}
	writeNativeUintptr(objectPtr, this)
	handler.refs.Add(1)
	return 0
}

func activateAddRef(this uintptr) uintptr {
	handler, ok := activationHandler(this)
	if !ok {
		return 0
	}
	return uintptr(handler.refs.Add(1))
}

func activateRelease(this uintptr) uintptr {
	handler, ok := activationHandler(this)
	if !ok {
		return 0
	}
	for {
		current := handler.refs.Load()
		if current == 0 {
			return 0
		}
		if handler.refs.CompareAndSwap(current, current-1) {
			return uintptr(current - 1)
		}
	}
}

func activateCompleted(this, operation uintptr) uintptr {
	handler, ok := activationHandler(this)
	if !ok || operation == 0 {
		return uintptr(uint32(0x80004003))
	}
	var activationHRESULT int32
	var client uintptr
	hr := callCOMAddress(operation, 3, uintptr(unsafe.Pointer(&activationHRESULT)), uintptr(unsafe.Pointer(&client)))
	var err error
	if callErr := result("process activation result", hr); callErr != nil {
		err = callErr
	} else if activationHRESULT < 0 {
		err = result("process activation", uintptr(uint32(activationHRESULT)))
	}
	if err != nil && client != 0 {
		releaseCOMAddress(client)
		client = 0
	}
	select {
	case handler.done <- activationResult{client: client, err: err}:
	default:
		if client != 0 {
			releaseCOMAddress(client)
		}
	}
	return 0
}

func activateProcessAudioClient(ctx context.Context, identity processIdentity) (uintptr, error) {
	capabilities := GetCapabilities()
	if !capabilities.ProcessLoopback {
		return 0, &ProcessCaptureError{Kind: ProcessCaptureUnsupported, Message: capabilities.ProcessLoopbackReason}
	}
	paramsMemory, paramsSize, err := allocateActivationParams(identity.PID)
	if err != nil {
		return 0, err
	}
	variantMemory, err := allocateActivationVariant(paramsMemory, paramsSize)
	if err != nil {
		coFree.Call(paramsMemory)
		return 0, err
	}
	devicePath, err := windows.UTF16PtrFromString(virtualAudioDeviceProcessLoopback)
	if err != nil {
		freeActivationMemory(paramsMemory, variantMemory)
		return 0, err
	}
	handler, err := newActivateCompletionHandler()
	if err != nil {
		freeActivationMemory(paramsMemory, variantMemory)
		return 0, err
	}
	var operation uintptr
	hr, _, _ := activateAudioInterfaceAsync.Call(
		uintptr(unsafe.Pointer(devicePath)),
		uintptr(unsafe.Pointer(&iidClient)),
		variantMemory,
		handler.object,
		uintptr(unsafe.Pointer(&operation)),
	)
	if callErr := result("activate process loopback", hr); callErr != nil {
		if operation != 0 {
			releaseCOMAddress(operation)
		}
		handler.free()
		freeActivationMemory(paramsMemory, variantMemory)
		return 0, callErr
	}
	cleanup := func() {
		if operation != 0 {
			releaseCOMAddress(operation)
		}
		freeActivationMemory(paramsMemory, variantMemory)
		handler.free()
		runtime.KeepAlive(handler)
	}
	select {
	case res := <-handler.done:
		cleanup()
		return res.client, res.err
	case <-ctx.Done():
		go func() {
			res := <-handler.done
			if res.client != 0 {
				releaseCOMAddress(res.client)
			}
			cleanup()
		}()
		return 0, ctx.Err()
	}
}

func allocateActivationParams(pid uint32) (uintptr, uint32, error) {
	params := audioClientActivationParams{ActivationType: processLoopbackActivation, TargetPID: pid, LoopbackMode: processLoopbackIncludeTree}
	size := unsafe.Sizeof(params)
	memory, err := allocateNative(size)
	if err != nil {
		return 0, 0, err
	}
	copyToNative(memory, unsafe.Pointer(&params), size)
	return memory, uint32(size), nil
}

func allocateActivationVariant(data uintptr, size uint32) (uintptr, error) {
	variant := propVariantBlob{VT: vtBlob, Size: size, Data: data}
	memory, err := allocateNative(unsafe.Sizeof(variant))
	if err != nil {
		return 0, err
	}
	copyToNative(memory, unsafe.Pointer(&variant), unsafe.Sizeof(variant))
	return memory, nil
}

func allocateNative(size uintptr) (uintptr, error) {
	memory, _, _ := coAlloc.Call(size)
	if memory == 0 {
		return 0, fmt.Errorf("allocate process loopback COM memory")
	}
	return memory, nil
}

func freeActivationMemory(params, variant uintptr) {
	if variant != 0 {
		coFree.Call(variant)
	}
	if params != 0 {
		coFree.Call(params)
	}
}

func copyToNative(destination uintptr, source unsafe.Pointer, size uintptr) {
	rtlMoveMemory.Call(destination, uintptr(source), size)
}

func copyFromNative(destination unsafe.Pointer, source uintptr, size uintptr) {
	rtlMoveMemory.Call(uintptr(destination), source, size)
}

func writeNativeUintptr(destination, value uintptr) {
	copyToNative(destination, unsafe.Pointer(&value), unsafe.Sizeof(value))
}

func readNativeUintptr(source uintptr) uintptr {
	var value uintptr
	copyFromNative(unsafe.Pointer(&value), source, unsafe.Sizeof(value))
	return value
}

func callCOMAddress(object uintptr, index uintptr, args ...uintptr) uintptr {
	if object == 0 {
		return uintptr(uint32(0x80004003))
	}
	vtable := readNativeUintptr(object)
	if vtable == 0 {
		return uintptr(uint32(0x80004003))
	}
	method := readNativeUintptr(vtable + index*unsafe.Sizeof(uintptr(0)))
	callArgs := make([]uintptr, 1, len(args)+1)
	callArgs[0] = object
	callArgs = append(callArgs, args...)
	result, _, _ := syscall.SyscallN(method, callArgs...)
	return result
}

func releaseCOMAddress(object uintptr) {
	if object != 0 {
		callCOMAddress(object, 2)
	}
}

type propVariantBlob struct {
	VT        uint16
	Reserved1 uint16
	Reserved2 uint16
	Reserved3 uint16
	Size      uint32
	Padding   uint32
	Data      uintptr
}

type waveFormatPCM struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	ExtraSize      uint16
}

func processCaptureFormat() (waveFormatPCM, Format) {
	wave := waveFormatPCM{FormatTag: 1, Channels: 2, SamplesPerSec: 44100, BitsPerSample: 16}
	wave.BlockAlign = wave.Channels * wave.BitsPerSample / 8
	wave.AvgBytesPerSec = wave.SamplesPerSec * uint32(wave.BlockAlign)
	return wave, Format{Rate: int(wave.SamplesPerSec), Channels: int(wave.Channels), Bits: int(wave.BitsPerSample), Align: int(wave.BlockAlign)}
}

func captureProcess(ctx context.Context, cfg domain.AudioSourceConfig, emit func(Block)) error {
	if cfg.Process == nil {
		return &ProcessCaptureError{Kind: ProcessCaptureUnavailable, Message: "process-only capture requires a process selection"}
	}
	done, err := apartment()
	if err != nil {
		return err
	}
	defer done()
	identity, err := resolveConfiguredProcess(cfg.Process)
	if err != nil {
		return err
	}
	processHandle, err := holdProcessIdentity(identity)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(processHandle)
	client, err := activateProcessAudioClient(ctx, identity)
	if err != nil {
		return err
	}
	if client == 0 {
		return fmt.Errorf("process loopback returned no audio client")
	}
	defer releaseCOMAddress(client)

	wave, format := processCaptureFormat()
	flags := uintptr(audclntStreamflagsLoopback | audclntStreamflagsEventCallback | audclntStreamflagsAutoconvertPCM)
	if err := result("initialize process loopback", callCOMAddress(client, 3, 0, flags, 0, 0, uintptr(unsafe.Pointer(&wave)), 0)); err != nil {
		return err
	}
	var capture uintptr
	if err := result("process capture service", callCOMAddress(client, 14, uintptr(unsafe.Pointer(&iidCapture)), uintptr(unsafe.Pointer(&capture)))); err != nil {
		return err
	}
	if capture == 0 {
		return fmt.Errorf("process capture service unavailable")
	}
	defer releaseCOMAddress(capture)

	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return fmt.Errorf("process capture event: %w", err)
	}
	defer windows.CloseHandle(event)
	if err := result("process capture event handle", callCOMAddress(client, 13, uintptr(event))); err != nil {
		return err
	}
	if err := result("start process capture", callCOMAddress(client, 10)); err != nil {
		return err
	}
	defer callCOMAddress(client, 11)

	emit(Block{Rate: format.Rate, At: time.Now(), ProcessID: identity.PID, ProcessPath: identity.Path, ProcessStartedAt: identity.StartedAt})
	lastPacket := time.Now()
	var captureClock captureClock
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		wait, waitErr := windows.WaitForSingleObject(event, 50)
		if waitErr != nil {
			return fmt.Errorf("wait process capture event: %w", waitErr)
		}
		now := time.Now()
		if err := ensureProcessAlive(processHandle); err != nil {
			return err
		}
		if wait == uint32(windows.WAIT_TIMEOUT) {
			if now.Sub(lastPacket) > 100*time.Millisecond {
				emit(Block{Rate: format.Rate, At: now, ProcessID: identity.PID, ProcessStartedAt: identity.StartedAt})
				lastPacket = now
			}
			continue
		}
		if wait != 0 {
			return fmt.Errorf("unexpected process capture wait result %d", wait)
		}
		for {
			var frames uint32
			if err := result("process packet size", callCOMAddress(capture, 5, uintptr(unsafe.Pointer(&frames)))); err != nil {
				return err
			}
			if frames == 0 {
				break
			}
			block, ready, err := readCapturePacket(capture, frames, format, now, &captureClock)
			if err != nil {
				return err
			}
			if !ready {
				break
			}
			block.ProcessID = identity.PID
			block.ProcessPath = identity.Path
			block.ProcessStartedAt = identity.StartedAt
			lastPacket = now
			emit(block)
		}
	}
}

func readCapturePacket(capture uintptr, frames uint32, format Format, now time.Time, clock *captureClock) (Block, bool, error) {
	var data *byte
	var count, flag uint32
	var position, qpc uint64
	if err := result("get buffer", callCOMAddress(capture, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&flag)), uintptr(unsafe.Pointer(&position)), uintptr(unsafe.Pointer(&qpc)))); err != nil {
		return Block{}, false, err
	}
	releaseBuffer := func() error {
		return result("release buffer", callCOMAddress(capture, 4, uintptr(count)))
	}
	if count == 0 {
		if err := releaseBuffer(); err != nil {
			return Block{}, false, err
		}
		return Block{}, false, nil
	}
	if count > uint32(format.Rate*2) {
		_ = releaseBuffer()
		return Block{}, false, fmt.Errorf("audio packet exceeds safe allocation")
	}
	flags := decodeWASAPIBufferFlags(flag)
	var raw []byte
	if !flags.Silent {
		if data == nil {
			_ = releaseBuffer()
			return Block{}, false, fmt.Errorf("audio packet pointer missing")
		}
		raw = append([]byte(nil), unsafe.Slice(data, int(count)*format.Align)...)
	}
	if err := releaseBuffer(); err != nil {
		return Block{}, false, err
	}
	samples, err := MonoPCM(raw, int(count), format, flags.Silent)
	if err != nil {
		return Block{}, false, err
	}
	return Block{
		Samples: samples, Rate: format.Rate,
		At:             clock.PacketStart(qpc, int(count), format.Rate, now, flags.TimestampError),
		Silent:         flags.Silent,
		Discontinuity:  flags.Discontinuity,
		TimestampError: flags.TimestampError,
	}, true, nil
}

// ensure propVariantBlob matches the 64-bit Windows PROPVARIANT VT_BLOB layout.
var _ [24 - int(unsafe.Sizeof(propVariantBlob{}))]byte
var _ [int(unsafe.Sizeof(propVariantBlob{})) - 24]byte
