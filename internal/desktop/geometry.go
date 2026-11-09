package desktop

// Studio window size in device-independent pixels (96 DPI). The process is
// per-monitor DPI aware, so Win32 sizes are physical pixels and must be scaled,
// otherwise a 4K/200% display gets a quarter-size window with cramped layout.
const (
	studioWidthDIP     = 1320
	studioHeightDIP    = 880
	studioMinWidthDIP  = 860
	studioMinHeightDIP = 560
	studioWorkMargin   = 24
)

type workArea struct{ Left, Top, Right, Bottom int }

type studioGeometry struct {
	X, Y, Width, Height int
	MinWidth, MinHeight int
}

// studioGeometryFor centres a DPI-scaled Studio window inside the work area.
// ok is false when the work area is too small to place it meaningfully; the
// caller then keeps the system default placement.
func studioGeometryFor(area workArea, dpi int) (studioGeometry, bool) {
	if dpi <= 0 {
		dpi = 96
	}
	scale := func(v int) int { return v * dpi / 96 }
	areaWidth, areaHeight := area.Right-area.Left, area.Bottom-area.Top
	width := min(scale(studioWidthDIP), areaWidth-studioWorkMargin)
	height := min(scale(studioHeightDIP), areaHeight-studioWorkMargin)
	if width < 480 || height < 360 {
		return studioGeometry{}, false
	}
	return studioGeometry{
		X:         area.Left + (areaWidth-width)/2,
		Y:         area.Top + (areaHeight-height)/2,
		Width:     width,
		Height:    height,
		MinWidth:  min(scale(studioMinWidthDIP), width),
		MinHeight: min(scale(studioMinHeightDIP), height),
	}, true
}
