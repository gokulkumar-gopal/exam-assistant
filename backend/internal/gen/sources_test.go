package gen

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// --- fixtures ---

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x + y), 255})
		}
	}
	return img
}

func jpegBytes(t *testing.T, img image.Image, q int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func dims(t *testing.T, b []byte) (int, int, string) {
	t.Helper()
	c, f, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return c.Width, c.Height, f
}

// withOrientation inserts an APP1 Exif segment (big- or little-endian) after the SOI.
func withOrientation(t *testing.T, jpg []byte, orientation uint16, little bool) []byte {
	t.Helper()
	var bo binary.AppendByteOrder = binary.BigEndian
	hdr := []byte("MM")
	if little {
		bo, hdr = binary.LittleEndian, []byte("II")
	}
	tiff := append([]byte{}, hdr...)
	tiff = bo.AppendUint16(tiff, 42)
	tiff = bo.AppendUint32(tiff, 8)
	tiff = bo.AppendUint16(tiff, 1) // one entry
	tiff = bo.AppendUint16(tiff, 0x0112)
	tiff = bo.AppendUint16(tiff, 3) // SHORT
	tiff = bo.AppendUint32(tiff, 1)
	tiff = bo.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0)
	tiff = bo.AppendUint32(tiff, 0)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	out = binary.BigEndian.AppendUint16(out, uint16(len(seg)+2))
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// bombPNG is a valid PNG header that claims w x h pixels followed by a tiny body.
func bombPNG(w, h uint32) []byte {
	chunk := func(typ string, data []byte) []byte {
		b := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
		b = append(b, typ...)
		b = append(b, data...)
		return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	}
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 2, 0, 0, 0) // 8-bit RGB
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, chunk("IHDR", ihdr)...)
	out = append(out, chunk("IDAT", []byte{0x78, 0x9c, 0x63, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01})...)
	return append(out, chunk("IEND", nil)...)
}

func doc(id, name, ct string) store.Document {
	return store.Document{ID: id, Name: name, ContentType: ct, Status: store.DocReady}
}

// --- PrepareSources ---

func TestSourcesStableOrderAndLabels(t *testing.T) {
	docs := []store.Document{doc("d3", "c.txt", "text/plain"), doc("d1", "a.txt", "text/plain"), doc("d2", "b.txt", "text/plain")}
	blobs := map[string][]byte{"d1": []byte("one"), "d2": []byte("two"), "d3": []byte("three")}
	src, l2d, failed := PrepareSources(docs, blobs)
	if len(failed) != 0 || len(src) != 3 {
		t.Fatal(src, failed)
	}
	for i, want := range []struct{ label, text, id string }{{"doc-1", "one", "d1"}, {"doc-2", "two", "d2"}, {"doc-3", "three", "d3"}} {
		if src[i].Label != want.label || string(src[i].Bytes) != want.text || src[i].ContentType != "text/plain" || l2d[want.label].ID != want.id {
			t.Errorf("source %d = %+v, map %+v", i, src[i], l2d[want.label])
		}
	}
	// Input order and duplicates do not change the result.
	src2, _, _ := PrepareSources([]store.Document{docs[2], docs[1], docs[0], docs[0]}, blobs)
	if len(src2) != 3 || src2[0].Label != "doc-1" || string(src2[0].Bytes) != "one" {
		t.Errorf("order not stable: %+v", src2)
	}
}

func TestLabelsAreDenseAfterFailures(t *testing.T) {
	docs := []store.Document{doc("a", "a.txt", "text/plain"), doc("b", "b.txt", "text/plain"), doc("c", "c.txt", "text/plain")}
	src, l2d, failed := PrepareSources(docs, map[string][]byte{"a": []byte("x"), "c": []byte("z")}) // b has no blob
	if len(src) != 2 || src[1].Label != "doc-2" || l2d["doc-2"].ID != "c" || len(failed) != 1 || failed[0].DocID != "b" || failed[0].Reason == "" {
		t.Fatal(src, failed)
	}
}

func TestLabelsNeverContainFileNames(t *testing.T) {
	secret := "Maths-Marks-Alice-Smith.txt"
	docs := []store.Document{doc("01ABC", secret, "text/plain")}
	src, _, _ := PrepareSources(docs, map[string][]byte{"01ABC": []byte("hello")})
	if len(src) != 1 {
		t.Fatal(src)
	}
	for _, s := range []string{src[0].Label, src[0].ContentType} {
		if strings.Contains(s, "Alice") || strings.Contains(s, "01ABC") || strings.Contains(s, ".txt") {
			t.Errorf("leak in %q", s)
		}
	}
	if !strings.HasPrefix(src[0].Label, "doc-") {
		t.Error(src[0].Label)
	}
}

func TestTXTInvalidUTF8Unreadable(t *testing.T) {
	src, _, failed := PrepareSources([]store.Document{doc("a", "a.txt", "text/plain")}, map[string][]byte{"a": []byte("ok \xff\xfe broken")})
	if len(src) != 0 || len(failed) != 1 || failed[0].DocID != "a" || failed[0].Name != "a.txt" || failed[0].Reason == "" {
		t.Fatal(src, failed)
	}
}

func TestTXTControlCharsStripped(t *testing.T) {
	in := "Line1\r\nLine2\tTab\x00\x07\x1b[31m end\u200B\u202Eevil\u2066\uFEFF\U000E0041 done\u0085"
	src, _, failed := PrepareSources([]store.Document{doc("a", "a.txt", "text/plain")}, map[string][]byte{"a": []byte(in)})
	if len(failed) != 0 || len(src) != 1 {
		t.Fatal(failed)
	}
	got := string(src[0].Bytes)
	if want := "Line1\nLine2\tTab[31m endevil done"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestTXTKeepsJoinersAndScripts(t *testing.T) {
	in := "क्‍ष नमस्ते \u200c فارسی 😀 ½ x²"
	if got := CleanText(in); got != in {
		t.Errorf("CleanText altered legitimate text: %q", got)
	}
}

func TestTXTEmptyAfterCleaningIsUnreadable(t *testing.T) {
	src, _, failed := PrepareSources([]store.Document{doc("a", "a.txt", "text/plain")}, map[string][]byte{"a": []byte(" \x00\u200B\n\t ")})
	if len(src) != 0 || len(failed) != 1 {
		t.Fatal(src, failed)
	}
}

func TestNoReadableSourcesFailsWith(t *testing.T) {
	src, _, failed := PrepareSources([]store.Document{doc("a", "a.png", "image/png"), doc("b", "b.txt", "text/plain")},
		map[string][]byte{"a": []byte("not an image"), "b": {0xff, 0xfe}})
	if len(src) != 0 || len(failed) != 2 {
		t.Fatal(src, failed)
	}
	err := RequireSources(src)
	ue, ok := err.(*UserError)
	if !ok || ue.Message != "We couldn't read any of the selected files." {
		t.Fatalf("err = %#v", err)
	}
	if RequireSources([]SourceFile{{}}) != nil {
		t.Error("non-empty sources must pass")
	}
}

func TestPDFPassedThroughAndUnknownTypeFails(t *testing.T) {
	pdf := []byte("%PDF-1.7 body")
	src, _, failed := PrepareSources([]store.Document{doc("a", "a.pdf", "application/pdf"), doc("b", "b.pdf", "application/pdf"), doc("c", "c.exe", "application/x-msdownload")},
		map[string][]byte{"a": pdf, "b": []byte("<html>"), "c": []byte("MZ")})
	if len(src) != 1 || src[0].ContentType != "application/pdf" || !bytes.Equal(src[0].Bytes, pdf) || len(failed) != 2 {
		t.Fatal(src, failed)
	}
}

func TestImageSourceIsPrepared(t *testing.T) {
	big := pngBytes(t, gradient(3000, 500))
	src, _, failed := PrepareSources([]store.Document{doc("a", "a.png", "image/png")}, map[string][]byte{"a": big})
	if len(failed) != 0 || len(src) != 1 || src[0].ContentType != "image/jpeg" {
		t.Fatal(src, failed)
	}
	if w, _, f := dims(t, src[0].Bytes); w != 2000 || f != "jpeg" {
		t.Errorf("w=%d f=%s", w, f)
	}
	small := pngBytes(t, gradient(40, 40))
	src, _, _ = PrepareSources([]store.Document{doc("a", "a.png", "image/png")}, map[string][]byte{"a": small})
	if src[0].ContentType != "image/png" || !bytes.Equal(src[0].Bytes, small) {
		t.Error("small PNG should pass untouched with its own content type")
	}
}

func TestBombAndCorruptImagesFailTheSourceOnly(t *testing.T) {
	docs := []store.Document{doc("a", "a.png", "image/png"), doc("b", "b.jpg", "image/jpeg"), doc("c", "c.png", "image/png"), doc("d", "d.txt", "text/plain")}
	corrupt := jpegBytes(t, gradient(200, 200), 80)
	corrupt = corrupt[:len(corrupt)/2]
	blobs := map[string][]byte{"a": bombPNG(30000, 30000), "b": corrupt, "c": append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3), "d": []byte("fine")}
	src, _, failed := PrepareSources(docs, blobs)
	if len(src) != 1 || len(failed) != 3 {
		t.Fatalf("src=%d failed=%+v", len(src), failed)
	}
}

// --- Downscale ---

func TestDownscaleOversizedLandscape(t *testing.T) {
	out, err := Downscale(jpegBytes(t, gradient(3000, 1000), 90))
	if err != nil {
		t.Fatal(err)
	}
	w, h, f := dims(t, out)
	if w != 2000 || f != "jpeg" || h < 666 || h > 667 {
		t.Errorf("got %dx%d %s", w, h, f)
	}
}

func TestDownscaleOversizedPortraitPNG(t *testing.T) {
	out, err := Downscale(pngBytes(t, gradient(1000, 4000)))
	if err != nil {
		t.Fatal(err)
	}
	if w, h, f := dims(t, out); h != 2000 || w != 500 || f != "jpeg" {
		t.Errorf("got %dx%d %s", w, h, f)
	}
}

func TestDownscaleNeverUpscales(t *testing.T) {
	in := pngBytes(t, gradient(300, 100))
	out, err := Downscale(in)
	if err != nil {
		t.Fatal(err)
	}
	if w, h, _ := dims(t, out); w != 300 || h != 100 {
		t.Errorf("got %dx%d", w, h)
	}
}

func TestSmallJPEGUntouched(t *testing.T) {
	in := jpegBytes(t, gradient(800, 600), 90)
	out, err := Downscale(in)
	if err != nil || !bytes.Equal(in, out) {
		t.Fatalf("small JPEG was changed (err %v)", err)
	}
}

func TestExifRotationApplied(t *testing.T) {
	base := jpegBytes(t, gradient(300, 100), 95)
	for _, little := range []bool{false, true} {
		out, err := Downscale(withOrientation(t, base, 6, little))
		if err != nil {
			t.Fatal(err)
		}
		if w, h, _ := dims(t, out); w != 100 || h != 300 {
			t.Errorf("little=%v: got %dx%d, want 100x300", little, w, h)
		}
	}
	// Orientation 1 and unknown values leave the file alone.
	for _, o := range []uint16{1, 0, 9} {
		in := withOrientation(t, base, o, false)
		if out, err := Downscale(in); err != nil || !bytes.Equal(in, out) {
			t.Errorf("orientation %d changed the file (err %v)", o, err)
		}
	}
}

func TestExifOrientationMovesPixels(t *testing.T) {
	// 64x32, red 16x16 block top-left, blue elsewhere. Orientation 6 (rotate 90 CW) gives
	// 32x64 with the red block at the top-right.
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			c := color.RGBA{0, 0, 255, 255}
			if x < 16 && y < 16 {
				c = color.RGBA{255, 0, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	out, err := Downscale(withOrientation(t, jpegBytes(t, img, 100), 6, false))
	if err != nil {
		t.Fatal(err)
	}
	dec, _, _ := image.Decode(bytes.NewReader(out))
	if dec.Bounds().Dx() != 32 || dec.Bounds().Dy() != 64 {
		t.Fatalf("dims %v", dec.Bounds())
	}
	r1, _, b1, _ := dec.At(24, 8).RGBA() // inside the moved block
	r2, _, b2, _ := dec.At(8, 8).RGBA()  // where the block would be if not rotated
	if r1>>8 < 200 || b1>>8 > 80 || r2>>8 > 80 || b2>>8 < 200 {
		t.Errorf("block not at top-right: (24,8) r=%d b=%d, (8,8) r=%d b=%d", r1>>8, b1>>8, r2>>8, b2>>8)
	}
}

func TestDownscaleRotatedAndOversized(t *testing.T) {
	out, err := Downscale(withOrientation(t, jpegBytes(t, gradient(3000, 1000), 80), 6, false))
	if err != nil {
		t.Fatal(err)
	}
	if w, h, _ := dims(t, out); h != 2000 || w < 666 || w > 667 {
		t.Errorf("got %dx%d", w, h)
	}
}

func TestPNGOver4MBReencodedJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1600))
	rand.New(rand.NewSource(1)).Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	in := pngBytes(t, img)
	if len(in) <= 4<<20 {
		t.Fatalf("fixture is only %d bytes", len(in))
	}
	out, err := Downscale(in)
	if err != nil {
		t.Fatal(err)
	}
	if w, h, f := dims(t, out); f != "jpeg" || w != 1600 || h != 1600 {
		t.Errorf("got %dx%d %s", w, h, f)
	}
}

func TestDownscaleRejectsBombBeforeDecoding(t *testing.T) {
	_, err := Downscale(bombPNG(30000, 30000))
	if err == nil || !strings.Contains(err.Error(), "pixels") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Downscale(bombPNG(4000, 4000)); err != nil && strings.Contains(err.Error(), "pixels") {
		t.Errorf("16 MP must pass the pixel guard: %v", err) // fails later on the truncated body, not on the cap
	}
}

func TestDownscaleCorruptAndEmptyDoNotPanic(t *testing.T) {
	good := jpegBytes(t, gradient(300, 300), 80)
	for name, in := range map[string][]byte{"nil": nil, "junk": []byte("junkjunkjunk"), "truncated": good[:len(good)/3], "header only": good[:20]} {
		if _, err := Downscale(in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// A hostile EXIF block must not panic the parser.
	for n := 0; n < 40; n++ {
		ex := withOrientation(t, good, 6, n%2 == 0)
		_, _ = Downscale(ex[:min(len(ex), 4+n)])
	}
}

func TestToQuestionMapsLabelAndAssignsID(t *testing.T) {
	zero := 2
	r := RawQuestion{Marks: 2, Text: "t", Options: []string{"a", "b", "c", "d"}, CorrectIndex: &zero, Source: "doc-2"}
	l2d := map[string]store.Document{"doc-2": {ID: "D2", Name: "notes.pdf"}}
	q, ok := r.ToQuestion(l2d)
	if !ok || q.ID == "" || q.SourceDocID != "D2" || q.SourceDocName != "notes.pdf" || q.Marks != 2 || *q.CorrectIndex != 2 || len(q.Options) != 4 {
		t.Fatalf("%+v %v", q, ok)
	}
	*q.CorrectIndex = 0
	q.Options[0] = "changed"
	if *r.CorrectIndex != 2 || r.Options[0] != "a" {
		t.Error("ToQuestion aliases the raw question")
	}
	q2, _ := r.ToQuestion(l2d)
	if q2.ID == q.ID {
		t.Error("IDs must be unique")
	}
	if _, ok := (RawQuestion{Source: "doc-9"}).ToQuestion(l2d); ok {
		t.Error("unknown label must not convert")
	}
	if f, ok := (RawQuestion{Marks: 3, Text: "t", ModelAnswer: "m", Source: "doc-2"}).ToQuestion(l2d); !ok || f.ModelAnswer != "m" || f.Options != nil || f.CorrectIndex != nil {
		t.Errorf("%+v", f)
	}
}
