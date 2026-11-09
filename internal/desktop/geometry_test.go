package desktop

import "testing"

func TestStudioGeometryScalesWithDPI(t *testing.T) {
	area := workArea{Right: 3840, Bottom: 2100}
	g, ok := studioGeometryFor(area, 192)
	if !ok {
		t.Fatal("4K work area must fit Studio")
	}
	if g.Width != 2640 || g.Height != 1760 || g.MinWidth != 1720 || g.MinHeight != 1120 {
		t.Fatalf("200%% scaling not applied: %+v", g)
	}
	if g.X != (3840-2640)/2 || g.Y != (2100-1760)/2 {
		t.Fatalf("window not centred: %+v", g)
	}
}

func TestStudioGeometryFitsSmallWorkArea(t *testing.T) {
	area := workArea{Left: -1366, Top: 0, Right: 0, Bottom: 728}
	g, ok := studioGeometryFor(area, 96)
	if !ok {
		t.Fatal("1366x728 work area must fit Studio")
	}
	if g.Width != 1320 || g.Height != 728-24 {
		t.Fatalf("window exceeds work area: %+v", g)
	}
	if g.MinWidth > g.Width || g.MinHeight > g.Height {
		t.Fatalf("minimum size larger than window: %+v", g)
	}
	if g.X != -1366+(1366-1320)/2 || g.Y != 12 {
		t.Fatalf("negative-origin monitor not respected: %+v", g)
	}
	if g, ok := studioGeometryFor(workArea{Right: 1280, Bottom: 680}, 144); !ok || g.Width != 1280-24 || g.Height != 680-24 || g.MinWidth != 1256 || g.MinHeight != 656 {
		t.Fatalf("scaled minimum must be capped by the work area: %+v", g)
	}
}

func TestStudioGeometryRejectsTinyArea(t *testing.T) {
	if _, ok := studioGeometryFor(workArea{Right: 400, Bottom: 300}, 96); ok {
		t.Fatal("tiny work area must fall back to system placement")
	}
	if g, ok := studioGeometryFor(workArea{Right: 1920, Bottom: 1040}, 0); !ok || g.Width != 1320 {
		t.Fatalf("unknown DPI must default to 96: %+v", g)
	}
}
