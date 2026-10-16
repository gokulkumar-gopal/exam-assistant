package docs

import (
	"bytes"
	"unicode/utf8"
)

// txtSniffBytes is how much of a text file must be valid UTF-8.
const txtSniffBytes = 4096

var (
	pngMagic  = []byte("\x89PNG\r\n\x1a\n")
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
	pdfMagic  = []byte("%PDF-")
)

// Sniff reports whether the first bytes of a file fit the declared MIME type.
// first may be longer than needed; only the leading bytes are inspected.
func Sniff(first []byte, mime string) bool {
	switch mime {
	case "application/pdf":
		return bytes.HasPrefix(first, pdfMagic)
	case "image/png":
		return bytes.HasPrefix(first, pngMagic)
	case "image/jpeg":
		return bytes.HasPrefix(first, jpegMagic)
	case "text/plain":
		return validUTF8Prefix(first)
	}
	return false
}

// validUTF8Prefix checks the first 4 KB. When the file continues past the window, a
// rune cut in half by the window edge is ignored rather than treated as invalid.
func validUTF8Prefix(b []byte) bool {
	if len(b) <= txtSniffBytes {
		return utf8.Valid(b)
	}
	b = b[:txtSniffBytes]
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		if utf8.RuneStart(b[len(b)-i]) {
			if !utf8.FullRune(b[len(b)-i:]) {
				b = b[:len(b)-i]
			}
			break
		}
	}
	return utf8.Valid(b)
}
