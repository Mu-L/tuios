package vt

import (
	"image/color"
	"reflect"
	"runtime"
	"testing"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// oddColor is a colour type the packer does not know, so it has to intern.
type oddColor struct{ v uint8 }

func (c oddColor) RGBA() (r, g, b, a uint32) {
	return uint32(c.v) * 0x101, 0, 0, 0xffff
}

func TestPackedCellIs24Bytes(t *testing.T) {
	if got := unsafe.Sizeof(packedCell{}); got != 24 {
		t.Fatalf("packedCell is %d bytes, want 24", got)
	}
	if got := unsafe.Sizeof(uv.Cell{}); got < 100 {
		t.Fatalf("uv.Cell is %d bytes; the packing exists because it was 112", got)
	}
}

// styledLine is one of everything a cell can carry, followed by blanks.
func styledLine(width int) uv.Line {
	line := make(uv.Line, width)
	for i := range line {
		line[i] = uv.EmptyCell
	}
	line[0] = uv.Cell{Content: "a", Width: 1}
	line[1] = uv.Cell{Content: "漢", Width: 2}
	line[2] = uv.Cell{Content: "", Width: 0} // the wide rune's spacer
	line[3] = uv.Cell{Content: "é", Width: 1}
	line[4] = uv.Cell{Content: "🇬🇧", Width: 2}
	line[5] = uv.Cell{Content: "", Width: 0}
	line[6] = uv.Cell{Content: "b", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(3), Bg: ansi.IndexedColor(200)}}
	line[7] = uv.Cell{Content: "c", Width: 1, Style: uv.Style{Fg: ansi.TrueColor(0x123456), UnderlineColor: color.RGBA{R: 1, G: 2, B: 3, A: 255}, Underline: uv.UnderlineCurly, Attrs: uv.AttrBold | uv.AttrItalic}}
	line[8] = uv.Cell{Content: "d", Width: 1, Style: uv.Style{Bg: color.RGBA{R: 9, G: 8, B: 7, A: 128}}}
	line[9] = uv.Cell{Content: "e", Width: 1, Style: uv.Style{Fg: oddColor{7}}}
	line[10] = uv.Cell{Content: "f", Width: 1, Link: uv.Link{URL: "https://example.test", Params: "id=1"}}
	line[11] = uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: ansi.BasicColor(1)}} // a painted blank
	line[12] = uv.Cell{Content: "�", Width: 1}
	line[13] = uv.Cell{Content: "\x00", Width: 1}
	return line
}

func TestScrollbackRoundTripsEveryKindOfCell(t *testing.T) {
	const width = 40
	want := styledLine(width)
	sb := NewScrollback(4)
	sb.PushLine(want)

	got := sb.Line(0)
	if len(got) != width {
		t.Fatalf("line came back %d wide, want %d", len(got), width)
	}
	for x := range width {
		if !reflect.DeepEqual(got[x], want[x]) {
			t.Errorf("cell %d: got %#v, want %#v", x, got[x], want[x])
		}
	}
	if stored := len(sb.lines[0].cells); stored != 14 {
		t.Errorf("stored %d cells, want 14: the blank tail is not stored", stored)
	}
	if stored := sb.lines[0].width; stored != width {
		t.Errorf("stored width %d, want %d", stored, width)
	}
}

func TestScrollbackDoesNotAliasThePushedLine(t *testing.T) {
	sb := NewScrollback(4)
	line := styledLine(20)
	sb.PushLine(line)
	line[0].Content = "Z"
	if got := sb.Line(0)[0].Content; got != "a" {
		t.Fatalf("scrollback line changed to %q with the caller's line, want %q", got, "a")
	}
}

func TestScrollbackLineCacheFollowsTheRing(t *testing.T) {
	sb := NewScrollback(2)
	push := func(s string) {
		sb.PushLine(uv.Line{{Content: s, Width: 1}, uv.EmptyCell})
	}
	push("A")
	if got := sb.Line(0)[0].Content; got != "A" {
		t.Fatalf("line 0 is %q, want A", got)
	}
	push("B")
	push("C") // evicts A
	if got := sb.Line(0)[0].Content; got != "B" {
		t.Fatalf("line 0 is %q after the ring moved, want B", got)
	}
	if got := sb.Line(1)[0].Content; got != "C" {
		t.Fatalf("line 1 is %q, want C", got)
	}
	// Same index, same generation: the decoded line is shared.
	if a, b := sb.Line(0), sb.Line(0); &a[0] != &b[0] {
		t.Fatal("two reads of one line in one generation decoded twice")
	}
}

func TestScrollbackCacheIsBounded(t *testing.T) {
	sb := NewScrollback(cacheCap * 2)
	for i := range cacheCap * 2 {
		sb.PushLine(uv.Line{{Content: string(rune('a' + i%26)), Width: 1}})
	}
	for i := range cacheCap * 2 {
		_ = sb.Line(i)
	}
	if n := len(sb.cache); n > cacheCap {
		t.Fatalf("cache holds %d decoded lines after a walk of the ring, want at most %d", n, cacheCap)
	}
}

func TestScrollbackFullRingReusesEvictedStorage(t *testing.T) {
	sb := NewScrollback(8)
	line := uv.Line{{Content: "x", Width: 1}, {Content: "y", Width: 1}, uv.EmptyCell, uv.EmptyCell}
	for range 8 {
		sb.PushLine(line)
	}
	if got := testing.AllocsPerRun(100, func() { sb.PushLine(line) }); got > 0 {
		t.Fatalf("a push into a full ring allocates %.1f times, want 0", got)
	}
}

func TestBlankWideRunesCutByTheEdgeOnPackedLines(t *testing.T) {
	sb := NewScrollback(4)
	sb.PushLine(uv.Line{{Content: "a", Width: 1}, {Content: "漢", Width: 2, Style: uv.Style{Fg: ansi.BasicColor(2)}}, {Content: "", Width: 0}})
	sb.blankWideRunesCutByTheEdge(2)
	got := sb.Line(0)[1]
	if got.Content != " " || got.Width != 1 || got.Style.Fg != ansi.BasicColor(2) {
		t.Fatalf("cut cell is %#v, want a styled blank", got)
	}
}

// TestPackedScrollbackHoldsALineForItsContent is the reason for the packing:
// a thousand short lines on a wide terminal used to cost width times 112
// bytes each, 23 MB at 207 columns; packed and trimmed they cost about the
// five cells that are on them.
func TestPackedScrollbackHoldsALineForItsContent(t *testing.T) {
	const width, lines = 207, 1000
	line := make(uv.Line, width)
	for i := range line {
		line[i] = uv.EmptyCell
	}
	for i, r := range "12345" {
		line[i] = uv.Cell{Content: string(r), Width: 1}
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	sb := NewScrollback(lines)
	for range lines {
		sb.PushLine(line)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(sb)

	held := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	const unpacked = width * 112 * lines
	if held > unpacked/10 {
		t.Fatalf("%d lines of %d columns hold %d bytes, want under a tenth of the %d the unpacked cells took", lines, width, held, unpacked)
	}
	t.Logf("%d lines x %d columns, five cells each: %d KiB packed, %d KiB unpacked", lines, width, held/1024, unpacked/1024)
}
