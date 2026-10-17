package jobpilot

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxCVBytes matches cv.MaxUploadBytes in apps/api: a larger file would only
// be refused there after it had been read and sent.
const maxCVBytes = 12 << 20

// errCVDirUnset is the refusal when the owner has not chosen a folder. Uploading
// is off by default because the server runs with the owner's token on the
// owner's machine: without a folder, an agent told to by a web page or a job
// posting could send any file it can name.
var errCVDirUnset = errors.New("uploading a CV is off: set JOBPILOT_CV_DIR on the MCP server to the folder holding your CV files, restart the session, and pass a file from that folder. Until then, upload the CV in the web app")

// cvFile is a CV read from the allowed folder, ready to send.
type cvFile struct {
	name string
	data []byte
}

// readCVFile reads a .pdf or .docx from inside dir, and nothing else.
//
// The file is opened through an os.Root on dir, which resolves '..',
// symbolic links and Windows junctions while it opens, and refuses any that
// lead outside the folder. Checking a resolved path first and opening it
// afterwards would leave a gap in which a link could be swapped in.
func readCVFile(dir, name string) (cvFile, error) {
	if strings.TrimSpace(dir) == "" {
		return cvFile{}, errCVDirUnset
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return cvFile{}, errors.New("path is required: a .pdf or .docx file in the JOBPILOT_CV_DIR folder")
	}
	// A relative folder would resolve against whatever directory the host
	// started the server in, and "~" is not expanded, so either would read
	// from a folder nobody chose.
	if !filepath.IsAbs(dir) {
		return cvFile{}, fmt.Errorf("JOBPILOT_CV_DIR must be an absolute path, such as C:\\Users\\you\\CVs or /home/you/cvs, not %q (\"~\" is not expanded); set the full path and restart the session", dir)
	}
	root := filepath.Clean(dir)
	rel := filepath.Clean(name)
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		// An absolute path is accepted only as a spelling of a file in the
		// folder; os.Root takes paths relative to it.
		var err error
		if rel, err = filepath.Rel(root, rel); err != nil {
			return cvFile{}, errOutsideCVDir
		}
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return cvFile{}, errOutsideCVDir
	}
	ext := strings.ToLower(filepath.Ext(rel))
	if ext != ".pdf" && ext != ".docx" {
		return cvFile{}, errors.New("only a .pdf or .docx CV can be uploaded from here; other formats go through the web app")
	}

	folder, err := os.OpenRoot(root)
	if err != nil {
		return cvFile{}, fmt.Errorf("JOBPILOT_CV_DIR cannot be opened: %w", err)
	}
	defer folder.Close()
	// Opening a named pipe or a device can block, so anything but a regular
	// file is refused before it is opened. The check after opening stays the
	// authority, since the path can change in between.
	if info, err := folder.Stat(rel); err != nil {
		return cvFile{}, openError(rel, err)
	} else if !info.Mode().IsRegular() {
		return cvFile{}, errors.New("that path is not a regular file")
	}
	f, err := openInFolder(folder, rel)
	if err != nil {
		return cvFile{}, openError(rel, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return cvFile{}, err
	}
	if !info.Mode().IsRegular() {
		return cvFile{}, errors.New("that path is not a regular file")
	}
	if info.Size() == 0 {
		return cvFile{}, errors.New("that file is empty")
	}
	if info.Size() > maxCVBytes {
		return cvFile{}, fmt.Errorf("that file is %d bytes; the API takes a CV of at most %d MB", info.Size(), maxCVBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCVBytes+1))
	if err != nil {
		return cvFile{}, err
	}
	if len(data) > maxCVBytes {
		return cvFile{}, fmt.Errorf("that file grew past %d MB while it was read", maxCVBytes>>20)
	}
	// The extension alone proves nothing: any file can be renamed. A PDF
	// starts with %PDF- and a DOCX is a zip archive.
	if !looksLike(ext, data) {
		return cvFile{}, fmt.Errorf("that file is named %s but its contents are not a %s document", ext, strings.TrimPrefix(ext, "."))
	}
	return cvFile{name: filepath.Base(rel), data: data}, nil
}

// errOutsideCVDir also covers an absolute link that points back inside the
// folder: os.Root refuses every absolute link, whatever it points at.
var errOutsideCVDir = errors.New("that path is outside the JOBPILOT_CV_DIR folder, or goes through a link that leads out of it or is absolute; only files in that folder can be uploaded, and a link inside it must be relative")

// openInFolder opens a path for reading without blocking, so a pipe swapped
// in after the Stat cannot hang the call; the Stat after opening refuses it.
func openInFolder(folder *os.Root, rel string) (*os.File, error) {
	return folder.OpenFile(rel, os.O_RDONLY|openNonBlocking, 0)
}

// openError words os.Root's refusal of a path inside the folder.
func openError(rel string, err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("no file %q in the JOBPILOT_CV_DIR folder", filepath.ToSlash(rel))
	case strings.Contains(err.Error(), "escapes"):
		// os.Root's refusal of a link that leads out of the folder or is
		// absolute; its error type is not exported.
		return errOutsideCVDir
	}
	return fmt.Errorf("that file cannot be opened: %w", err)
}

func looksLike(ext string, data []byte) bool {
	switch ext {
	case ".pdf":
		// The PDF standard lets the header sit within the first KB.
		head := data[:min(len(data), 1024)]
		return bytes.Contains(head, []byte("%PDF-"))
	case ".docx":
		return bytes.HasPrefix(data, []byte("PK\x03\x04"))
	}
	return false
}
