package gen

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder for image.Decode
	"net/http"

	xdraw "golang.org/x/image/draw"
)

// Image preparation limits (AI-spec 5.1).
const (
	maxImageSide   = 2000
	reencodeBytes  = 4 << 20 // re-encode as JPEG above this size
	jpegQuality    = 85
	maxImagePixels = 40_000_000 // decoded-size guard against decompression bombs
)

// ErrImageTooLarge is returned for an image whose header claims more than maxImagePixels.
var ErrImageTooLarge = errors.New("image has too many pixels")

// Downscale prepares a JPEG or PNG for the model (AI-spec 5.1). The pixel count in the
// header is checked with image.DecodeConfig before anything is decoded, so a small file
// that claims 30000x30000 pixels is rejected without allocating. The EXIF orientation of
// a JPEG is applied, a longest side over 2000 px is scaled down (never up), and the result
// is re-encoded as JPEG (quality 85) when it was scaled, rotated, or is over 4 MB.
// Otherwise the input is returned unchanged. Scaling happens before rotation so the
// full-size image is never copied; the result is the same. Transparency is flattened on
// white when a PNG becomes a JPEG. The returned bytes are PNG or JPEG: see imageContentType.
func Downscale(img []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		return nil, fmt.Errorf("image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return nil, ErrImageTooLarge
	}
	orient := 1
	if format == "jpeg" {
		orient = exifOrientation(img)
	}
	longest := max(cfg.Width, cfg.Height)
	scale := longest > maxImageSide

	// Decode even when the file will be returned unchanged: a valid header over a corrupt
	// body must fail here, not at the provider. The pixel cap above bounds this.
	src, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, fmt.Errorf("image decode: %w", err)
	}
	if !scale && orient == 1 && len(img) <= reencodeBytes {
		return img, nil
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	var cur image.Image = src
	if scale {
		nw, nh := w*maxImageSide/longest, h*maxImageSide/longest
		nw, nh = max(nw, 1), max(nh, 1)
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		whiteBackground(dst)
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
		cur = dst
	}
	if orient > 1 {
		cur = orientImage(cur, orient)
	} else if _, isRGBA := cur.(*image.RGBA); !isRGBA {
		rgba := image.NewRGBA(cur.Bounds())
		whiteBackground(rgba)
		draw.Draw(rgba, rgba.Bounds(), cur, cur.Bounds().Min, draw.Over)
		cur = rgba
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, cur, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("image encode: %w", err)
	}
	return out.Bytes(), nil
}

func whiteBackground(img *image.RGBA) {
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
}

// imageContentType reports "image/jpeg" or "image/png" from the bytes.
func imageContentType(b []byte) string {
	if http.DetectContentType(b) == "image/png" {
		return "image/png"
	}
	return "image/jpeg"
}

// orientImage applies EXIF orientation 2 to 8 and returns an RGBA image.
func orientImage(src image.Image, o int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			default: // 8
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// exifOrientation reads the EXIF orientation tag (0x0112) of a JPEG, returning 1 when it is
// absent, not 2..8, or the data is malformed. It is a small bounds-checked parser written
// from the EXIF/TIFF layout, used instead of an EXIF library to avoid a dependency.
func exifOrientation(j []byte) int {
	if len(j) < 4 || j[0] != 0xFF || j[1] != 0xD8 {
		return 1
	}
	p := 2
	for p+4 <= len(j) && j[p] == 0xFF {
		marker := j[p+1]
		if marker == 0xDA || marker == 0xD9 { // image data or end: no EXIF after this
			return 1
		}
		size := int(binary.BigEndian.Uint16(j[p+2:]))
		if size < 2 || p+2+size > len(j) {
			return 1
		}
		seg := j[p+4 : p+2+size]
		if marker == 0xE1 && len(seg) >= 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		p += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	var bo binary.ByteOrder
	switch {
	case len(t) < 8:
		return 1
	case t[0] == 'I' && t[1] == 'I':
		bo = binary.LittleEndian
	case t[0] == 'M' && t[1] == 'M':
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:]) != 42 {
		return 1
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd:]))
	for i := 0; i < n; i++ {
		e := ifd + 2 + 12*i
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 && bo.Uint16(t[e+2:]) == 3 && bo.Uint32(t[e+4:]) == 1 {
			if v := int(bo.Uint16(t[e+8:])); v >= 2 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
