// Inline image rendering for the chat transcript.
//
// Images are placed with kitty unicode placeholders (U=1): the place
// sequence is followed by placeholder cells that anchor the image to the
// text grid. The image scrolls with content, dies with its cells, and can
// never smear across rows or sit under text — so there is no placement
// lifecycle to track, no delete pass, and no invalidation epoch. A raw
// transmit-and-place (a=T/a=p without U=1) was tried first and smeared on
// scroll: screen-cell placements outlive the text written over them.
//
// The payload is uploaded once per digest (a=t); every repaint only
// re-emits the ~50 byte place (a=p). Image-bearing blocks bypass the
// per-block render cache (see renderBlocks) so a cached string can never
// replay an upload on every frame.
package app

import (
	"strings"

	"pitago/src/components/chat"
	terminal_image "pitago/src/components/terminal_image"
)

// imageRenderState is render-only kitty bookkeeping. It lives behind a
// pointer on Model so it survives Model's value copies (View, renderBlocks
// inputs) while staying mutable.
type imageRenderState struct {
	up     map[uint64]uint64 // image digest -> kitty image id
	geo    map[uint64][3]int // image digest -> {cols, rows, widthCells used}
	png    map[uint64]string // image digest -> transcoded PNG base64 (non-png inputs)
	pngBad map[uint64]bool   // image digest -> transcode failed, don't retry per frame
}

func newImageRenderState() *imageRenderState {
	return &imageRenderState{up: map[uint64]uint64{}, geo: map[uint64][3]int{},
		png: map[uint64]string{}, pngBad: map[uint64]bool{}}
}

func (m *Model) imgState() *imageRenderState {
	if m.imgRender == nil {
		m.imgRender = newImageRenderState()
	}
	return m.imgRender
}

// allocImageID returns the lowest free image id, or 0 when the pool is
// exhausted (caller falls back to □). Placeholder foreground colors carry
// only 8 bits, so ids must stay in 1..255.
//
// The pool is deliberately never recycled: an id is bound to a digest for
// the life of the session, because a placeholder cell already on screen is
// only that digest's image. Re-minting an id would point an existing cell
// at another digest's pixels, and wiping the map would re-send every
// payload on every repaint. 255 distinct images per session is far past
// normal vision use; beyond it, □ is the honest answer.
func (st *imageRenderState) allocImageID() uint64 {
	used := map[uint64]bool{}
	for _, id := range st.up {
		used[id] = true
	}
	for id := uint64(1); id <= 255; id++ {
		if !used[id] {
			return id
		}
	}
	return 0
}

// imageGeom returns cached cell geometry for digest at widthCells, sizing
// once via terminal_image.Size. Geometry (not the digest) changes with the
// width, so the width rides along in the cache entry.
func (m *Model) imageGeom(data, mime string, digest uint64, widthCells int) (int, int, bool) {
	st := m.imgState()
	if g, ok := st.geo[digest]; ok && g[2] == widthCells && g[0] > 0 {
		return g[0], g[1], true
	}
	c, r := terminal_image.Size(data, mime, widthCells)
	if c < 1 || r < 1 {
		return 0, 0, false
	}
	if len(st.geo) > 128 {
		st.geo = map[uint64][3]int{}
	}
	st.geo[digest] = [3]int{c, r, widthCells}
	return c, r, true
}

// resolvePNG returns render-ready PNG bytes for any supported input mime:
// PNG passes through, jpeg/gif/webp transcode once and cache by digest (a
// failed transcode caches negative so a broken image costs one attempt,
// not one per frame).
func (m *Model) resolvePNG(img chat.Image) (string, bool) {
	if strings.EqualFold(img.Mime, "image/png") {
		if img.Data == "" {
			return "", false
		}
		return img.Data, true
	}
	st := m.imgState()
	if data, ok := st.png[img.Digest]; ok {
		return data, true
	}
	if st.pngBad[img.Digest] {
		return "", false
	}
	data, ok := terminal_image.ToPNG(img.Data, img.Mime)
	if !ok {
		st.pngBad[img.Digest] = true
		return "", false
	}
	if len(st.png) > 64 {
		st.png = map[uint64]string{}
		st.pngBad = map[uint64]bool{}
	}
	st.png[img.Digest] = data
	return data, true
}

// imageLines renders one image per reserved row block. The first row
// carries the place sequence (plus a one-time upload) followed by
// placeholder cells; continuation rows are placeholder cells only.
// Viewport padding after the placeholders is harmless — it starts beyond
// the image columns. Images that cannot be placed (non-kitty protocol,
// hidden by settings, or an unsupported/undecodable mime) keep the □
// fallback, exactly the historical behaviour.
func (m *Model) imageLines(images []chat.Image, lineWidth int) []string {
	if lineWidth < 1 {
		lineWidth = 1
	}
	var out []string
	kitty := m.ShowImages && m.ImageProtocol == terminal_image.Kitty
	for _, img := range images {
		c, r, ok := 0, 0, false
		w := min(m.ImageWidthCells, lineWidth)
		var data string
		if kitty && img.Data != "" {
			if data, ok = m.resolvePNG(img); ok {
				c, r, ok = m.imageGeom(data, "image/png", img.Digest, w)
			}
		}
		if !ok {
			out = append(out, terminal_image.Fallback())
			continue
		}
		st := m.imgState()
		id, uploaded := st.up[img.Digest]
		if !uploaded {
			id = st.allocImageID()
			if id == 0 {
				out = append(out, terminal_image.Fallback())
				continue
			}
			st.up[img.Digest] = id
		}
		firstRow, ok := terminal_image.PlaceholderRow(id, 0, c)
		if !ok {
			out = append(out, terminal_image.Fallback())
			continue
		}
		first := terminal_image.PlaceUnicode(id, c, r) + firstRow
		if !uploaded {
			first = terminal_image.Upload(data, id) + first
		}
		out = append(out, first)
		for i := 1; i < r; i++ {
			row, ok := terminal_image.PlaceholderRow(id, i, c)
			if !ok {
				break
			}
			out = append(out, row)
		}
	}
	return out
}
