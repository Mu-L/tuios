package vt

import (
	"image/color"
	"reflect"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// DefaultScrollbackSize is the default number of lines to keep in the
// scrollback buffer.
const DefaultScrollbackSize = 10000

// Scrollback is the ring of lines that have scrolled off the top of the
// screen. It is a ring so a push is O(1) once the ring is full.
//
// Lines are stored packed, not as uv.Line. A uv.Cell is 112 bytes: a string
// header, three colour interfaces, a link of two strings and an int width. A
// scrollback line kept in that form costs terminal width times 112 bytes no
// matter what is on it, so at the default 10000 lines a 207-column pane held
// 232 MB the moment its ring filled, and the daemon and every attached client
// each held their own copy. A packedCell is 24 bytes, and a line is stored
// only up to its last cell that is not blank, so a line of a build log costs
// what is written on it. Reads decode back to uv.Line, padded to the width
// the line had, through a small cache so a render of a scrolled pane, which
// asks for the same line once per column, decodes each line once.
type Scrollback struct {
	// lines is the ring.
	lines []packedLine
	// maxLines is the ring's capacity.
	maxLines int
	// head is the index of the oldest line.
	head int
	// tail is the index the next line lands at.
	tail int
	// full is set once tail has caught up with head.
	full bool
	// onTrim is called when the ring overwrites its oldest line. The
	// argument is the number of lines dropped.
	onTrim func(int)

	// Intern tables for what a packedCell cannot hold in 4 bytes: grapheme
	// clusters of more than one rune, hyperlinks, and colour values of a type
	// the packer does not know. Each is capped at internCap entries; past
	// the cap a cell degrades (first rune only, no link, colour reduced to
	// RGB) rather than growing without bound on a guest that manufactures
	// distinct values.
	graphemes   []string
	graphemeIdx map[string]uint32
	links       []uv.Link
	linkIdx     map[uv.Link]uint32
	colors      []color.Color
	colorIdx    map[color.Color]uint32

	// cache holds decoded lines by index for the current generation. Every
	// mutation bumps gen, which empties it on the next read. It is capped at
	// cacheCap entries so a walk of the whole ring cannot pin a decoded copy
	// of it.
	cache    map[int]uv.Line
	cacheGen uint64
	gen      uint64
}

// internCap bounds each intern table. Past it, the packer degrades the cell
// instead of adding an entry.
const internCap = 1 << 20

// cacheCap bounds the decoded-line cache: a few screens of rows.
const cacheCap = 256

// packedLine is one stored line: its cells up to the last one that is not
// blank, and the width it had, so a read comes back at the original length.
type packedLine struct {
	cells []packedCell
	width int32
}

// packedCell is a uv.Cell in 24 bytes.
type packedCell struct {
	// content is a rune, or contentInterned|index into Scrollback.graphemes.
	// Zero is the empty string, which is what a wide cell's spacer holds.
	content uint32
	// fg, bg, ul are packed colours; see packColor.
	fg, bg, ul uint32
	// link is 0 for none, otherwise 1+index into Scrollback.links.
	link      uint32
	attrs     uint8
	underline uint8
	width     uint8
	_         uint8
}

const contentInterned = 1 << 31

// Colour packing: the top three bits say what the low 29 hold.
const (
	colorShift = 29
	colorMask  = 1<<colorShift - 1

	colorNil      = 0 << colorShift
	colorBasic    = 1 << colorShift
	colorIndexed  = 2 << colorShift
	colorTrue     = 3 << colorShift // ansi.TrueColor, 24-bit RGB
	colorRGB      = 4 << colorShift // color.RGBA with alpha 255, 24-bit RGB
	colorInterned = 5 << colorShift
)

// NewScrollback creates a scrollback ring of maxLines lines. Zero or less
// means DefaultScrollbackSize.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	return &Scrollback{
		lines:    make([]packedLine, maxLines),
		maxLines: maxLines,
	}
}

// SetOnTrim sets a callback that fires when the ring overwrites oldest lines.
func (sb *Scrollback) SetOnTrim(fn func(int)) {
	sb.onTrim = fn
}

// PushLine stores a copy of line as the newest scrollback line. The caller
// keeps line and may go on writing to it: nothing here aliases it.
//
// Into a full ring the push reuses the storage of the line it evicts when
// that is large enough, so a steady flood of similar lines allocates nothing.
func (sb *Scrollback) PushLine(line uv.Line) {
	if len(line) == 0 {
		return
	}

	n := len(line)
	for n > 0 && isBlankCell(&line[n-1]) {
		n--
	}

	var cells []packedCell
	if sb.full {
		cells = sb.lines[sb.tail].cells[:0]
	}
	if cap(cells) < n {
		cells = make([]packedCell, n)
	} else {
		cells = cells[:n]
	}
	for i := range n {
		sb.pack(&line[i], &cells[i])
	}
	sb.lines[sb.tail] = packedLine{cells: cells, width: int32(len(line))}

	sb.tail = (sb.tail + 1) % sb.maxLines
	if sb.full {
		sb.head = (sb.head + 1) % sb.maxLines
		if sb.onTrim != nil {
			sb.onTrim(1)
		}
	}
	if sb.tail == sb.head {
		sb.full = true
	}
	sb.gen++
}

// isBlankCell reports whether c is a plain space: the cell a line's unused
// tail is made of, which the ring does not store.
func isBlankCell(c *uv.Cell) bool {
	return c.Content == " " && c.Width == 1 &&
		c.Style.Fg == nil && c.Style.Bg == nil && c.Style.UnderlineColor == nil &&
		c.Style.Underline == 0 && c.Style.Attrs == 0 &&
		c.Link.URL == "" && c.Link.Params == ""
}

func (sb *Scrollback) pack(c *uv.Cell, dst *packedCell) {
	dst.content = sb.packContent(c.Content)
	dst.fg = sb.packColor(c.Style.Fg)
	dst.bg = sb.packColor(c.Style.Bg)
	dst.ul = sb.packColor(c.Style.UnderlineColor)
	dst.link = 0
	if c.Link.URL != "" || c.Link.Params != "" {
		dst.link = sb.internLink(c.Link)
	}
	dst.attrs = c.Style.Attrs
	dst.underline = uint8(c.Style.Underline)
	dst.width = uint8(max(0, min(c.Width, 255)))
}

func (sb *Scrollback) unpack(pc *packedCell, dst *uv.Cell) {
	dst.Content = sb.unpackContent(pc.content)
	dst.Style = uv.Style{
		Fg:             sb.unpackColor(pc.fg),
		Bg:             sb.unpackColor(pc.bg),
		UnderlineColor: sb.unpackColor(pc.ul),
		Underline:      uv.Underline(pc.underline),
		Attrs:          pc.attrs,
	}
	dst.Link = uv.Link{}
	if pc.link != 0 {
		dst.Link = sb.links[pc.link-1]
	}
	dst.Width = int(pc.width)
}

func (sb *Scrollback) packContent(s string) uint32 {
	if s == "" {
		return 0
	}
	r, size := utf8.DecodeRuneInString(s)
	// A single valid rune is stored as itself. U+FFFD is interned so a real
	// replacement character and a malformed byte stay distinguishable, and
	// NUL is interned because zero means the empty string.
	if size == len(s) && r != utf8.RuneError && r != 0 {
		return uint32(r)
	}
	if sb.graphemeIdx == nil {
		sb.graphemeIdx = make(map[string]uint32)
	}
	if i, ok := sb.graphemeIdx[s]; ok {
		return contentInterned | i
	}
	if len(sb.graphemes) >= internCap {
		// Table full: keep the first rune.
		if r == 0 || r == utf8.RuneError {
			return ' '
		}
		return uint32(r)
	}
	sb.graphemes = append(sb.graphemes, s)
	i := uint32(len(sb.graphemes) - 1)
	sb.graphemeIdx[s] = i
	return contentInterned | i
}

func (sb *Scrollback) unpackContent(v uint32) string {
	if v == 0 {
		return ""
	}
	if v&contentInterned != 0 {
		return sb.graphemes[v&^contentInterned]
	}
	return string(rune(v))
}

func (sb *Scrollback) internLink(l uv.Link) uint32 {
	if sb.linkIdx == nil {
		sb.linkIdx = make(map[uv.Link]uint32)
	}
	if i, ok := sb.linkIdx[l]; ok {
		return i + 1
	}
	if len(sb.links) >= internCap {
		return 0
	}
	sb.links = append(sb.links, l)
	i := uint32(len(sb.links) - 1)
	sb.linkIdx[l] = i
	return i + 1
}

// packColor packs the colour types the emulator produces into 4 bytes and
// interns anything else.
func (sb *Scrollback) packColor(c color.Color) uint32 {
	switch v := c.(type) {
	case nil:
		return colorNil
	case ansi.BasicColor:
		return colorBasic | uint32(v)
	case ansi.IndexedColor:
		return colorIndexed | uint32(v)
	case ansi.TrueColor:
		return colorTrue | uint32(v)&0xffffff
	case color.RGBA:
		if v.A == 0xff {
			return colorRGB | uint32(v.R)<<16 | uint32(v.G)<<8 | uint32(v.B)
		}
	}
	return sb.internColor(c)
}

func (sb *Scrollback) internColor(c color.Color) uint32 {
	comparable := reflect.TypeOf(c).Comparable()
	if comparable {
		if sb.colorIdx == nil {
			sb.colorIdx = make(map[color.Color]uint32)
		}
		if i, ok := sb.colorIdx[c]; ok {
			return colorInterned | i
		}
	}
	if len(sb.colors) >= internCap || !comparable {
		// No slot for it: keep what it looks like.
		r, g, b, _ := c.RGBA()
		return colorRGB | (r>>8)<<16 | (g>>8)<<8 | b>>8
	}
	sb.colors = append(sb.colors, c)
	i := uint32(len(sb.colors) - 1)
	sb.colorIdx[c] = i
	return colorInterned | i
}

func (sb *Scrollback) unpackColor(v uint32) color.Color {
	payload := v & colorMask
	switch v &^ colorMask {
	case colorBasic:
		return ansi.BasicColor(payload)
	case colorIndexed:
		return ansi.IndexedColor(payload)
	case colorTrue:
		return ansi.TrueColor(payload)
	case colorRGB:
		return color.RGBA{R: uint8(payload >> 16), G: uint8(payload >> 8), B: uint8(payload), A: 0xff}
	case colorInterned:
		return sb.colors[payload]
	}
	return nil
}

// Len returns the number of lines in the ring.
func (sb *Scrollback) Len() int {
	if sb.full {
		return sb.maxLines
	}
	if sb.tail >= sb.head {
		return sb.tail - sb.head
	}
	return sb.maxLines - sb.head + sb.tail
}

// Line returns the line at index, oldest first, decoded to its original
// width, or nil when index is out of range. The result is shared with later
// calls for the same index until the ring changes, so callers must not write
// to it.
func (sb *Scrollback) Line(index int) uv.Line {
	if index < 0 || index >= sb.Len() {
		return nil
	}
	if sb.cacheGen != sb.gen || len(sb.cache) >= cacheCap {
		clear(sb.cache)
		sb.cacheGen = sb.gen
	}
	if line, ok := sb.cache[index]; ok {
		return line
	}
	line := sb.decode(&sb.lines[(sb.head+index)%sb.maxLines])
	if sb.cache == nil {
		sb.cache = make(map[int]uv.Line)
	}
	sb.cache[index] = line
	return line
}

func (sb *Scrollback) decode(pl *packedLine) uv.Line {
	line := make(uv.Line, pl.width)
	for i := range pl.cells {
		sb.unpack(&pl.cells[i], &line[i])
	}
	for i := len(pl.cells); i < len(line); i++ {
		line[i] = uv.EmptyCell
	}
	return line
}

// Lines returns every line in the ring, oldest first, each decoded fresh.
func (sb *Scrollback) Lines() []uv.Line {
	length := sb.Len()
	if length == 0 {
		return nil
	}
	result := make([]uv.Line, length)
	for i := range length {
		result[i] = sb.decode(&sb.lines[(sb.head+i)%sb.maxLines])
	}
	return result
}

// Clear drops every line and the storage behind it.
func (sb *Scrollback) Clear() {
	count := sb.Len()
	sb.head = 0
	sb.tail = 0
	sb.full = false
	clear(sb.lines)
	sb.gen++
	if sb.onTrim != nil && count > 0 {
		sb.onTrim(count)
	}
}

// blankWideRunesCutByTheEdge clears a double-width rune that a narrowing
// resize leaves in what is now the last column of a scrollback line, for the
// reason Screen.blankWideRunesCutByTheEdge gives.
func (sb *Scrollback) blankWideRunesCutByTheEdge(newWidth int) {
	x := newWidth - 1
	if x < 0 {
		return
	}
	for i := range sb.Len() {
		pl := &sb.lines[(sb.head+i)%sb.maxLines]
		if x >= len(pl.cells) || pl.cells[x].width <= 1 {
			continue
		}
		pl.cells[x].content = ' '
		pl.cells[x].width = 1
		pl.cells[x].link = 0
		sb.gen++
	}
}

// MaxLines returns the ring's capacity.
func (sb *Scrollback) MaxLines() int {
	return sb.maxLines
}

// SetMaxLines resizes the ring, keeping the newest lines that fit.
func (sb *Scrollback) SetMaxLines(maxLines int) {
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	if maxLines == sb.maxLines {
		return
	}

	oldLen := sb.Len()
	newLines := make([]packedLine, maxLines)
	newLen := min(oldLen, maxLines)
	startIndex := oldLen - newLen // drop the oldest when shrinking
	for i := range newLen {
		newLines[i] = sb.lines[(sb.head+startIndex+i)%sb.maxLines]
	}

	sb.lines = newLines
	sb.maxLines = maxLines
	sb.head = 0
	sb.tail = newLen % maxLines
	sb.full = newLen == maxLines
	sb.gen++

	if oldLen > newLen && sb.onTrim != nil {
		sb.onTrim(oldLen - newLen)
	}
}

// extractLine copies row y of buf into a fresh line of the given width.
func extractLine(buf *uv.Buffer, y, width int) uv.Line {
	line := make(uv.Line, width)
	for x := range width {
		if cell := buf.CellAt(x, y); cell != nil {
			line[x] = *cell
		} else {
			line[x] = uv.EmptyCell
		}
	}
	return line
}
