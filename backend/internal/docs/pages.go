package docs

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// txtCharsPerPage is the TXT page size used for "pages" (backend-spec 8.3).
const txtCharsPerPage = 3000

// ErrUnreadable means the file's content could not be paged (corrupt, encrypted, empty).
var ErrUnreadable = errors.New("docs: unreadable document")

func init() {
	// Stop pdfcpu from creating a config directory (Lambda's file system is read-only).
	model.ConfigPath = "disable"
}

// CountPages returns the page count: PDF via pdfcpu, images 1, TXT ceil(runes/3000) (min 1).
// A PDF that cannot be read yields an error wrapping ErrUnreadable; it never panics.
func CountPages(mime string, data []byte) (int, error) {
	switch mime {
	case "application/pdf":
		return pdfPages(data)
	case "image/png", "image/jpeg":
		return 1, nil
	case "text/plain":
		n := (utf8.RuneCount(data) + txtCharsPerPage - 1) / txtCharsPerPage
		return max(n, 1), nil
	}
	return 0, fmt.Errorf("docs: no page counter for %q", mime)
}

func pdfPages(data []byte) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			n, err = 0, fmt.Errorf("%w: pdf library panic: %v", ErrUnreadable, r)
		}
	}()
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	n, err = api.PageCount(bytes.NewReader(data), conf)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	if n < 1 {
		return 0, fmt.Errorf("%w: no pages", ErrUnreadable)
	}
	return n, nil
}
