// Package cv turns an uploaded CV file into text and then into structured
// candidate facts.
//
// Extraction and parsing are deterministic. A model is never needed to read a
// CV: the file formats are machine-readable, and guessing at fields the file
// does not contain is exactly what this product must not do.
package cv

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// Supported upload formats.
const (
	FormatPDF      = "pdf"
	FormatDOCX     = "docx"
	FormatText     = "text"
	FormatMarkdown = "markdown"
)

// MaxUploadBytes bounds an uploaded CV. Real CVs are well under this; the limit
// exists so a malformed or hostile upload cannot exhaust memory.
const MaxUploadBytes = 12 << 20

var (
	ErrEmptyFile       = errors.New("the uploaded file is empty")
	ErrTooLarge        = fmt.Errorf("the uploaded file exceeds %d MB", MaxUploadBytes>>20)
	ErrUnknownFormat   = errors.New("unsupported CV format: upload a PDF, DOCX, Markdown, or plain text file")
	ErrNoTextExtracted = errors.New("no text could be extracted; the file may be a scanned image, which this product does not OCR")
)

// DetectFormat identifies the format from the file's own bytes, falling back to
// the filename. Content wins: a mislabelled extension should not decide how a
// file is parsed.
func DetectFormat(name string, data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return FormatPDF
	case bytes.HasPrefix(data, []byte("PK\x03\x04")) && looksLikeDOCX(data):
		return FormatDOCX
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return FormatPDF
	case strings.HasSuffix(lower, ".docx"):
		return FormatDOCX
	case strings.HasSuffix(lower, ".md"), strings.HasSuffix(lower, ".markdown"):
		return FormatMarkdown
	case strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".text"):
		return FormatText
	}
	if isMostlyText(data) {
		return FormatText
	}
	return ""
}

// looksLikeDOCX distinguishes a Word document from any other zip archive.
func looksLikeDOCX(data []byte) bool {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	for _, file := range reader.File {
		if file.Name == "word/document.xml" {
			return true
		}
	}
	return false
}

func isMostlyText(data []byte) bool {
	sample := data
	if len(sample) > 2048 {
		sample = sample[:2048]
	}
	if len(sample) == 0 {
		return false
	}
	printable := 0
	for _, b := range sample {
		if b == '\n' || b == '\r' || b == '\t' || (b >= 0x20 && b != 0x7f) {
			printable++
		}
	}
	return printable*10 >= len(sample)*9
}

// ExtractText converts an uploaded CV to plain text. The returned format tells
// the caller how the text was obtained, which the UI shows so the user can tell
// whether a poor parse is their file or our reader.
func ExtractText(name string, data []byte) (text string, format string, err error) {
	if len(data) == 0 {
		return "", "", ErrEmptyFile
	}
	if len(data) > MaxUploadBytes {
		return "", "", ErrTooLarge
	}
	format = DetectFormat(name, data)
	switch format {
	case FormatPDF:
		text, err = extractPDF(data)
	case FormatDOCX:
		text, err = extractDOCX(data)
	case FormatText, FormatMarkdown:
		text = string(data)
	default:
		return "", "", ErrUnknownFormat
	}
	if err != nil {
		return "", format, err
	}
	text = Clean(text)
	if strings.TrimSpace(text) == "" {
		return "", format, ErrNoTextExtracted
	}
	return text, format, nil
}

// extractDOCX reads word/document.xml directly. A .docx is a zip of XML, so no
// third-party dependency is warranted here.
func extractDOCX(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("this DOCX could not be read: %w", err)
	}
	var document *zip.File
	for _, file := range reader.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return "", errors.New("this DOCX has no word/document.xml")
	}
	handle, err := document.Open()
	if err != nil {
		return "", err
	}
	defer handle.Close()

	var out strings.Builder
	decoder := xml.NewDecoder(io.LimitReader(handle, MaxUploadBytes))
	for {
		token, tokenErr := decoder.Token()
		if tokenErr == io.EOF {
			break
		}
		if tokenErr != nil {
			return "", fmt.Errorf("this DOCX is malformed: %w", tokenErr)
		}
		switch element := token.(type) {
		case xml.CharData:
			out.Write(element)
		case xml.StartElement:
			// w:tab and w:br are whitespace; w:p ends a paragraph.
			switch element.Name.Local {
			case "tab":
				out.WriteByte(' ')
			case "br", "cr":
				out.WriteByte('\n')
			}
		case xml.EndElement:
			if element.Name.Local == "p" {
				out.WriteByte('\n')
			}
		}
	}
	return out.String(), nil
}

var multiSpace = regexp.MustCompile(`[ \t\x{00a0}]+`)
var manyBlankLines = regexp.MustCompile(`\n{3,}`)

// Clean normalises extracted text: consistent line endings, no runs of blank
// lines, and the ligatures and private-use glyphs PDF fonts commonly emit.
func Clean(text string) string {
	text = strings.NewReplacer(
		"\r\n", "\n", "\r", "\n",
		"ﬀ", "ff", "ﬁ", "fi", "ﬂ", "fl", "ﬃ", "ffi", "ﬄ", "ffl",
		"‘", "'", "’", "'", "“", `"`, "”", `"`,
		"–", "-", "—", "-", "•", "- ", "·", "- ",
	).Replace(text)

	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.Is(unicode.Co, r): // private-use glyphs from subset fonts
			b.WriteRune(' ')
		case unicode.Is(unicode.Cf, r): // BOM, zero-width and other format marks
			continue
		case unicode.IsControl(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	text = multiSpace.ReplaceAllString(b.String(), " ")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	text = strings.Join(lines, "\n")
	return strings.TrimSpace(manyBlankLines.ReplaceAllString(text, "\n\n"))
}
