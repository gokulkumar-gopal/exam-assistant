package gen

import (
	"errors"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// This file prepares the job's documents for the model (AI-spec 5.1, 5.2; backend-spec 7.3).
// It is pure: the worker loads the bytes from the blob store (owner-checked) and passes
// them in, and nothing here does I/O.

// FailedSource is a document that could not be prepared and is not sent (AI-spec 5.1).
type FailedSource = store.FailedSource

// ErrNoReadableSources is returned by RequireSources when nothing could be prepared.
var ErrNoReadableSources = &UserError{Message: NoSourcesMessage}

// RequireSources fails with the user-facing NoSourcesMessage when no source remains.
func RequireSources(sources []SourceFile) error {
	if len(sources) == 0 {
		return ErrNoReadableSources
	}
	return nil
}

// PrepareSources turns documents and their bytes (blobs, keyed by document ID) into the
// sources sent to the model, in a stable order (sorted by document ID, duplicates ignored)
// and labelled doc-1, doc-2, ... counting only the sources that were prepared, so labels
// are dense. The label never contains the file name or the document ID (AI-spec 5.2);
// labelToDoc maps it back and lives only for the job. A document that is missing from
// blobs, of an unsupported type, or that fails preparation goes to failed with a reason.
func PrepareSources(documents []store.Document, blobs map[string][]byte) (sources []SourceFile, labelToDoc map[string]store.Document, failed []FailedSource) {
	ordered := slices.Clone(documents)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	ordered = slices.CompactFunc(ordered, func(a, b store.Document) bool { return a.ID == b.ID })

	labelToDoc = map[string]store.Document{}
	for _, d := range ordered {
		fail := func(reason string) {
			failed = append(failed, FailedSource{DocID: d.ID, Name: d.Name, Reason: reason})
		}
		data, ok := blobs[d.ID]
		if !ok {
			fail(missingFileReason)
			continue
		}
		ct, out, reason, err := prepareOne(d.ContentType, data)
		if err != nil {
			// The cause can name the file's contents or sizes only, never keys; log without the name.
			slog.Warn("gen: source not prepared", "doc_id", d.ID, "err", err)
			fail(reason)
			continue
		}
		label := "doc-" + strconv.Itoa(len(sources)+1)
		sources = append(sources, SourceFile{Label: label, ContentType: ct, Bytes: out})
		labelToDoc[label] = d
	}
	return sources, labelToDoc, failed
}

type prepError string

func (e prepError) Error() string { return string(e) }

const tooLargeImageReason = "This image is too large to read."

// prepareOne returns the content type and bytes to send, or an error with the reason to
// show the user.
func prepareOne(contentType string, data []byte) (ct string, out []byte, reason string, err error) {
	switch contentType {
	case "application/pdf":
		// Sent as it is: the model reads text and page images (AI-spec 5.1).
		if !docs.Sniff(data, contentType) {
			return "", nil, unreadableFileReason, prepError("not a PDF")
		}
		return contentType, data, "", nil
	case "image/jpeg", "image/png":
		out, err := Downscale(data)
		if err != nil {
			if errors.Is(err, ErrImageTooLarge) {
				return "", nil, tooLargeImageReason, err
			}
			return "", nil, unreadableFileReason, err
		}
		return imageContentType(out), out, "", nil
	case "text/plain":
		if !utf8.Valid(data) {
			return "", nil, unreadableFileReason, prepError("text is not valid UTF-8")
		}
		text := CleanText(string(data))
		if strings.TrimSpace(text) == "" {
			return "", nil, unreadableFileReason, prepError("text is empty")
		}
		return contentType, []byte(text), "", nil
	}
	return "", nil, unreadableFileReason, prepError("unsupported type " + contentType)
}

// CleanText removes what is not text a student could read, from uploaded TXT files and
// from the Avoid list, so it cannot hide instructions from a reader or confuse the layout
// (AI-spec 5.1, 9.1). It keeps \n and \t, turns \r\n and \r into \n, and drops:
//   - control characters (C0, DEL and C1, which includes U+0085);
//   - bidirectional overrides and isolates (U+202A to U+202E, U+2066 to U+2069, U+200E,
//     U+200F, U+061C), which can make text read differently from how it is stored;
//   - zero-width space, word joiner and byte order mark (U+200B, U+2060, U+FEFF);
//   - Unicode tag characters (U+E0000 to U+E007F), an invisible way to smuggle text.
//
// U+200C and U+200D are kept because Hindi, Persian and emoji need them to be spelled.
func CleanText(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r):
			return -1
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069,
			r == 0x200E, r == 0x200F, r == 0x061C, r == 0x200B, r == 0x2060, r == 0xFEFF,
			r >= 0xE0000 && r <= 0xE007F:
			return -1
		}
		return r
	}, s)
}
