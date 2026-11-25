package main

// What a token is stored as (owner, Nov 25: "convert and downsize them to 192x192"): a token is shown at 192 pixels across at most, so what a GM or
// the administrator uploads is stored as a WebP of at most tokenStoredPx a side (normalizeToken, read by putMonsterToken for both routes): a
// larger picture is scaled down by averaging the source pixels each new pixel covers, a PNG, JPEG or WebP is read (the WebP decoder and
// writer are libraries: the standard library has neither), and whatever else the file carried (camera, place, software tags) is not kept. The
// WebP is lossless, so a token's edges and transparency stay exact. What was stored, and from what size, is told in the upload's reply.
// convertStoredTokens (below) brings the tokens stored before this rule, as PNGs up to 512 pixels, to it once.

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // image.Decode reads these three
	_ "image/png"
	"log"

	"github.com/HugoSmits86/nativewebp"
	_ "golang.org/x/image/webp"
)

// tokenStoredPx: the largest side a stored token keeps (the stage and the stat block show it at 192 or less).
const tokenStoredPx = 192

// normalizeToken returns the picture to store, its type, its side in pixels and a sentence saying what was done ("" when it is stored as it
// came). The picture has passed checkImage (a square PNG, JPEG or WebP in the token rule's sizes). A WebP that is already small enough is kept
// as it is: re-writing it would only make it larger.
func normalizeToken(imageBytes []byte, mime string) (stored []byte, storedMime string, px int, note string, err error) {
	src, _, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, "", 0, "", fmt.Errorf("the picture could not be read: %w", err)
	}
	from := src.Bounds().Dx()
	if mime == "image/webp" && from <= tokenStoredPx {
		return imageBytes, mime, from, "", nil
	}
	out := src
	if from > tokenStoredPx {
		out = scaleSquare(src, tokenStoredPx)
	}
	var b bytes.Buffer
	if err := nativewebp.Encode(&b, out, nil); err != nil {
		return nil, "", 0, "", err
	}
	px = out.Bounds().Dx()
	switch {
	case from > tokenStoredPx:
		note = fmt.Sprintf("Stored as a %d by %d WebP (it was %d by %d, %d KB, now %d KB): a token is shown at that size or smaller.", px, px, from, from, (len(imageBytes)+1023)/1024, (b.Len()+1023)/1024)
	default:
		note = "Stored as a WebP."
	}
	return b.Bytes(), "image/webp", px, note, nil
}

// convertStoredTokens: once, at the start, brings every token stored before the 192 pixel WebP rule (its token_px is empty) to it: read, scaled,
// re-written and saved through saveMonsterToken, which records the side so it is never looked at again. A token that can't be read is left as
// it is and its side recorded as 0 (it is logged). It changes only what a token is stored as, never whose it is or which monster has it.
func convertStoredTokens() error {
	rows, err := db.Query(`SELECT key, token, COALESCE(token_type, '') FROM custom_monsters WHERE token IS NOT NULL AND token_px IS NULL`)
	if err != nil {
		return err
	}
	type old struct {
		key, mime string
		img       []byte
	}
	var todo []old
	for rows.Next() {
		var o old
		if err := rows.Scan(&o.key, &o.img, &o.mime); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	var before, after int
	for _, o := range todo {
		stored, mime, px, _, err := normalizeToken(o.img, o.mime)
		if err != nil {
			log.Printf("Token of %s left as it is: %v", o.key, err)
			if _, err := db.Exec(`UPDATE custom_monsters SET token_px = 0 WHERE key = $1`, o.key); err != nil {
				return err
			}
			continue
		}
		if err := saveMonsterToken(o.key, stored, mime, px); err != nil {
			return err
		}
		before, after = before+len(o.img), after+len(stored)
	}
	if len(todo) > 0 {
		log.Printf("Monster tokens: %d converted to %d pixel WebP (%d KB to %d KB)", len(todo), tokenStoredPx, before/1024, after/1024)
	}
	return nil
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
