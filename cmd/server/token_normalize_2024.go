package main

// What a token is stored as (owner, Nov 24): a token is shown at 192 pixels across at most, so what a GM or the administrator uploads is
// stored as a PNG of at most tokenStoredPx a side (normalizeToken, read by putMonsterToken for both routes): a larger picture is scaled down
// by averaging the source pixels each new pixel covers (no new dependency: the standard library has no scaler), a JPEG becomes a PNG, and
// whatever else the file carried (camera, place, software tags) is not kept. A WebP is stored as it came: the server has no WebP decoder or
// encoder, and a WebP is already compact. What was stored, and from what size, is told in the upload's reply.

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
)

// tokenStoredPx: the largest side a stored token keeps (the stage and the stat block show it much smaller).
const tokenStoredPx = 512

// normalizeToken returns the picture to store, its type and a sentence saying what was done ("" when it is stored as it came). The picture has
// passed checkImage (a square PNG, JPEG or WebP in the token rule's sizes).
func normalizeToken(imageBytes []byte, mime string) (stored []byte, storedMime, note string, err error) {
	if mime == "image/webp" {
		return imageBytes, mime, "", nil
	}
	src, _, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, "", "", fmt.Errorf("the picture could not be read: %w", err)
	}
	from := src.Bounds().Dx()
	out := src
	if from > tokenStoredPx {
		out = scaleSquare(src, tokenStoredPx)
	}
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&b, out); err != nil {
		return nil, "", "", err
	}
	switch {
	case from > tokenStoredPx:
		note = fmt.Sprintf("Stored as a %d by %d PNG (it was %d by %d, %d KB): a token is shown much smaller than that.", tokenStoredPx, tokenStoredPx, from, from, (len(imageBytes)+1023)/1024)
	case mime == "image/jpeg":
		note = "Stored as a PNG."
	}
	return b.Bytes(), "image/png", note, nil
}

// scaleSquare scales a square picture down to side pixels by area averaging: each new pixel is the average of the source area it covers
// (partly covered source pixels weighted by how much), with transparency weighted in so a transparent edge doesn't bleed its colour.
func scaleSquare(src image.Image, side int) *image.RGBA {
	b := src.Bounds()
	in := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy())) // premultiplied RGBA: averaging it weights by alpha
	draw.Draw(in, in.Bounds(), src, b.Min, draw.Src)
	n := b.Dx()
	out := image.NewRGBA(image.Rect(0, 0, side, side))
	ratio := float64(n) / float64(side)
	for y := 0; y < side; y++ {
		y0, y1 := float64(y)*ratio, float64(y+1)*ratio
		for x := 0; x < side; x++ {
			x0, x1 := float64(x)*ratio, float64(x+1)*ratio
			var r, g, bl, a, weight float64
			for sy := int(y0); sy < n && float64(sy) < y1; sy++ {
				wy := overlap(float64(sy), float64(sy+1), y0, y1)
				for sx := int(x0); sx < n && float64(sx) < x1; sx++ {
					w := wy * overlap(float64(sx), float64(sx+1), x0, x1)
					i := sy*in.Stride + sx*4
					r += w * float64(in.Pix[i])
					g += w * float64(in.Pix[i+1])
					bl += w * float64(in.Pix[i+2])
					a += w * float64(in.Pix[i+3])
					weight += w
				}
			}
			if weight == 0 {
				continue
			}
			o := y*out.Stride + x*4
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = round8(r/weight), round8(g/weight), round8(bl/weight), round8(a/weight)
		}
	}
	return out
}

// overlap: how much of the interval [a0, a1] lies inside [b0, b1].
func overlap(a0, a1, b0, b1 float64) float64 {
	lo, hi := a0, a1
	if b0 > lo {
		lo = b0
	}
	if b1 < hi {
		hi = b1
	}
	if hi < lo {
		return 0
	}
	return hi - lo
}

func round8(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return uint8(v + 0.5)
}
