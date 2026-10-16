package jobpilot

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// cvUpload is one POST /api/cv the fake API received.
type cvUpload struct {
	filename, contentType, label, auth string
	data                               []byte
	fields                             int
}

// The fixtures are generated: a few bytes with the right signature, never a
// real CV.
var fakePDF = []byte("%PDF-1.4\n% generated test fixture, not a CV\n%%EOF\n")

// approvedPDF is the file the fake API lists as cv-1, the user's approved CV.
var approvedPDF = []byte("%PDF-1.4\n% generated test fixture: the approved CV, not a real one\n%%EOF\n")

func fakeDOCX(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, err := z.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<w:document><w:body><w:p><w:r><w:t>Generated fixture</w:t></w:r></w:p></w:body></w:document>`))
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// cvFolders lays out an allowed folder and, beside it, a folder the tool must
// never read from, each holding a valid-looking PDF.
func cvFolders(t *testing.T) (allowed, outside string) {
	t.Helper()
	base := t.TempDir()
	allowed, outside = filepath.Join(base, "cvs"), filepath.Join(base, "private")
	for _, dir := range []string{allowed, outside, filepath.Join(allowed, "variants")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(allowed, "cv.pdf"), fakePDF)
	write(filepath.Join(allowed, "UPPER.PDF"), fakePDF)
	write(filepath.Join(allowed, "variants", "backend.docx"), fakeDOCX(t))
	write(filepath.Join(allowed, "notes.txt"), []byte("plain text"))
	write(filepath.Join(allowed, "renamed.pdf"), []byte("not a pdf at all"))
	write(filepath.Join(allowed, "renamed.docx"), fakePDF)
	write(filepath.Join(allowed, "empty.pdf"), nil)
	write(filepath.Join(outside, "secret.pdf"), fakePDF)
	if err := os.Mkdir(filepath.Join(allowed, "folder.pdf"), 0o755); err != nil {
		t.Fatal(err)
	}
	big, err := os.Create(filepath.Join(allowed, "big.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = big.Write(fakePDF)
	if err = big.Truncate(maxCVBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = big.Close()
	return allowed, outside
}

func TestReadCVFileReadsOnlyFromTheAllowedFolder(t *testing.T) {
	allowed, _ := cvFolders(t)
	for _, tc := range []struct {
		name, path, wantName string
	}{
		{"a file name", "cv.pdf", "cv.pdf"},
		{"a subfolder", filepath.Join("variants", "backend.docx"), "backend.docx"},
		{"the full path inside the folder", filepath.Join(allowed, "cv.pdf"), "cv.pdf"},
		{"a '..' that stays inside", filepath.Join("variants", "..", "cv.pdf"), "cv.pdf"},
		{"an upper-case extension", "UPPER.PDF", "UPPER.PDF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readCVFile(allowed, tc.path)
			if err != nil {
				t.Fatalf("readCVFile(%q): %v", tc.path, err)
			}
			if got.name != tc.wantName || len(got.data) == 0 {
				t.Fatalf("got %q with %d bytes, want %q", got.name, len(got.data), tc.wantName)
			}
		})
	}
}

func TestReadCVFileRefuses(t *testing.T) {
	allowed, outside := cvFolders(t)
	for _, tc := range []struct {
		name, dir, path, want string
	}{
		{"no folder configured", "", "cv.pdf", "JOBPILOT_CV_DIR"},
		{"a blank folder setting", "   ", "cv.pdf", "JOBPILOT_CV_DIR"},
		{"an empty path", allowed, " ", "path is required"},
		{"the folder itself", allowed, ".", "outside"},
		{"'..' out of the folder", allowed, filepath.Join("..", "private", "secret.pdf"), "outside"},
		{"'..' that leaves and comes back past the root", allowed, filepath.Join("variants", "..", "..", "private", "secret.pdf"), "outside"},
		{"an absolute path outside", allowed, filepath.Join(outside, "secret.pdf"), "outside"},
		{"another extension", allowed, "notes.txt", ".pdf or .docx"},
		{"no extension", allowed, "cv", ".pdf or .docx"},
		{"a renamed file", allowed, "renamed.pdf", "not a pdf"},
		{"a PDF named .docx", allowed, "renamed.docx", "not a docx"},
		{"an empty file", allowed, "empty.pdf", "empty"},
		{"a folder named like a CV", allowed, "folder.pdf", "not a regular file"},
		{"a file over the API's limit", allowed, "big.pdf", "at most 12 MB"},
		{"a missing file", allowed, "gone.pdf", "no file"},
		{"a missing folder", filepath.Join(allowed, "nope"), "cv.pdf", "cannot be opened"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readCVFile(tc.dir, tc.path)
			if err == nil {
				t.Fatalf("readCVFile(%q, %q) read %d bytes; it must refuse", tc.dir, tc.path, len(got.data))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("readCVFile(%q, %q) = %v, want it to mention %q", tc.dir, tc.path, err, tc.want)
			}
		})
	}
}

// A link inside the folder must not lead the tool out of it. Creating a
// symbolic link needs a privilege Windows rarely grants, so that case is
// skipped there and the junction test below stands in for it.
func TestReadCVFileRefusesALinkOutOfTheFolder(t *testing.T) {
	allowed, outside := cvFolders(t)
	if err := os.Symlink(filepath.Join(outside, "secret.pdf"), filepath.Join(allowed, "link.pdf")); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(allowed, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("cv.pdf", filepath.Join(allowed, "alias.pdf")); err != nil {
		t.Fatal(err)
	}
	// Relative targets are resolved, '..' included, rather than refused for
	// their form as absolute ones are.
	if err := os.Symlink(filepath.Join("..", "private", "secret.pdf"), filepath.Join(allowed, "up.pdf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "private"), filepath.Join(allowed, "uplinked")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"link.pdf", filepath.Join("linked", "secret.pdf"), "up.pdf", filepath.Join("uplinked", "secret.pdf")} {
		if _, err := readCVFile(allowed, path); err == nil || !strings.Contains(err.Error(), "outside") {
			t.Errorf("readCVFile(%q) = %v; a link out of the folder must be refused", path, err)
		}
	}
	// A link that stays inside the folder is just another name for a CV in it.
	if got, err := readCVFile(allowed, "alias.pdf"); err != nil || !bytes.Equal(got.data, fakePDF) {
		t.Errorf("a link to a CV inside the folder should be read: %v", err)
	}
}

func TestReadCVFileRefusesAJunctionOutOfTheFolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows feature")
	}
	allowed, outside := cvFolders(t)
	junction := filepath.Join(allowed, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v: %s", err, out)
	}
	if _, err := readCVFile(allowed, filepath.Join("junction", "secret.pdf")); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("a junction out of the folder must be refused, got %v", err)
	}
}

// Windows spells paths in ways Linux has no counterpart for: another drive,
// a drive without a folder, a folder without a drive, and other letter case.
func TestReadCVFileWindowsPathShapes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path shapes")
	}
	allowed, _ := cvFolders(t)
	volume := filepath.VolumeName(allowed)
	other := "Z:"
	if strings.EqualFold(volume, other) {
		other = "Y:"
	}
	for _, path := range []string{
		other + `\cvs\cv.pdf`,
		volume + "cv.pdf",
		strings.TrimPrefix(allowed, volume) + `\cv.pdf`,
		`\\?\` + allowed + `\cv.pdf`,
	} {
		if got, err := readCVFile(allowed, path); err == nil {
			t.Errorf("readCVFile(%q) read %d bytes; it must refuse", path, len(got.data))
		}
	}
	// The same folder in other letter case is still the folder.
	if _, err := readCVFile(allowed, strings.ToUpper(allowed)+`\cv.pdf`); err != nil {
		t.Errorf("a path differing only in case should be read: %v", err)
	}
}

// The upload body is bytes, not JSON, and must go out whole again when a
// stale token earns a 401 and the client logs in once more.
func TestUploadCVRetriesWholeAfterA401(t *testing.T) {
	var attempts atomic.Int32
	var lastBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/login":
			_, _ = w.Write([]byte(`{"token":"fresh"}`))
		case "/api/cv":
			attempts.Add(1)
			if r.Header.Get("Authorization") != "Bearer fresh" {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(401)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				w.WriteHeader(400)
				return
			}
			data, _ := io.ReadAll(file)
			lastBody.Store(data)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"cv-up","approved":false}`))
		}
	}))
	defer srv.Close()
	c, err := NewClient(Options{BaseURL: srv.URL, Token: "stale", Email: "me@example.test", Password: "secret-pass"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.UploadCV(t.Context(), cvFile{name: "cv.pdf", data: fakePDF}, "")
	if err != nil || out.ID != "cv-up" {
		t.Fatalf("UploadCV after a 401: %+v, %v", out, err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("want the upload sent twice, got %d", attempts.Load())
	}
	if got, _ := lastBody.Load().([]byte); !bytes.Equal(got, fakePDF) {
		t.Fatalf("the retry carried %d bytes of the file, want %d", len(got), len(fakePDF))
	}
}

func TestUploadCVSendsTheFileToTheAPI(t *testing.T) {
	allowed, _ := cvFolders(t)
	cs, fake := connectOptions(t, Options{Token: "test-token", CVDir: allowed}, nil, nil, false, true)
	res, text := call(t, cs, "upload_cv", map[string]any{"path": "cv.pdf", "label": "Platform"})
	if res.IsError {
		t.Fatalf("upload_cv: %s", text)
	}
	fake.mu.Lock()
	uploads := fake.cvUploads
	fake.mu.Unlock()
	if len(uploads) != 1 {
		t.Fatalf("want one upload, got %d", len(uploads))
	}
	up := uploads[0]
	if up.filename != "cv.pdf" || up.contentType != "application/pdf" || up.label != "Platform" || !bytes.Equal(up.data, fakePDF) {
		t.Errorf("the API received %q (%s) labelled %q with %d bytes", up.filename, up.contentType, up.label, len(up.data))
	}
	if up.fields != 2 {
		t.Errorf("the form should carry only file and label, got %d fields", up.fields)
	}
	if up.auth != "Bearer test-token" {
		t.Errorf("the upload must carry the user's token, got %q", up.auth)
	}

	out := structured[map[string]any](t, res)
	if out["id"] != "cv-up" || out["approved"] != false {
		t.Errorf("the result should be the new, unapproved CV: %v", out)
	}
	parsed, _ := out["parsed"].(map[string]any)
	if warnings, _ := parsed["warnings"].([]any); len(warnings) != 1 || warnings[0] != "No phone number was found." {
		t.Errorf("the parse warnings must come back as the API wrote them, got %v", parsed["warnings"])
	}
	if _, ok := out["extracted_text"]; ok {
		t.Error("the extracted text belongs to get_cv, not to the upload result")
	}
	if _, ok := parsed["text"]; ok {
		t.Error("the parse's copy of the text should be dropped too")
	}

	// Without a label, none is sent, and a DOCX goes with its own type.
	if res, text = call(t, cs, "upload_cv", map[string]any{"path": filepath.Join("variants", "backend.docx")}); res.IsError {
		t.Fatalf("upload_cv docx: %s", text)
	}
	fake.mu.Lock()
	up = fake.cvUploads[1]
	fake.mu.Unlock()
	if up.label != "" || up.fields != 1 || !strings.Contains(up.contentType, "wordprocessingml") {
		t.Errorf("a DOCX without a label was sent as %s with label %q and %d fields", up.contentType, up.label, up.fields)
	}
}

func TestUploadCVRefusalsNeverReachTheAPI(t *testing.T) {
	allowed, outside := cvFolders(t)
	for _, tc := range []struct {
		name, dir, path, want string
	}{
		{"off by default", "", "cv.pdf", "JOBPILOT_CV_DIR"},
		{"outside the folder", allowed, filepath.Join(outside, "secret.pdf"), "outside"},
		{"another format", allowed, "notes.txt", ".pdf or .docx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, fake := connectOptions(t, Options{Token: "test-token", CVDir: tc.dir}, nil, nil, false, true)
			res, text := call(t, cs, "upload_cv", map[string]any{"path": tc.path})
			if !res.IsError || !strings.Contains(text, tc.want) {
				t.Fatalf("upload_cv(%q) should be refused mentioning %q, got %v: %s", tc.path, tc.want, res.IsError, text)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.cvUploads) != 0 {
				t.Fatalf("a refused upload reached the API %d times", len(fake.cvUploads))
			}
		})
	}
}

// The API replaces a CV uploaded again with the same bytes and clears its
// approval, which sends the applications using it back to review. upload_cv
// refuses that for an approved CV and changes nothing (#155).
func TestUploadCVRefusesTheFileOfAnApprovedCV(t *testing.T) {
	allowed, _ := cvFolders(t)
	if err := os.WriteFile(filepath.Join(allowed, "approved.pdf"), approvedPDF, 0o600); err != nil {
		t.Fatal(err)
	}
	uploads := func(fake *fakeAPI) int {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.cvUploads)
	}

	cs, fake := connectOptions(t, Options{Token: "test-token", CVDir: allowed}, nil, nil, false, true)
	res, text := call(t, cs, "upload_cv", map[string]any{"path": "approved.pdf"})
	if !res.IsError || !strings.Contains(text, "already") || !strings.Contains(text, "approved") || !strings.Contains(text, "cv-1") {
		t.Fatalf("uploading an approved CV's file should be refused, naming the CV: %v: %s", res.IsError, text)
	}
	if n := uploads(fake); n != 0 {
		t.Fatalf("the refused upload reached the API %d times", n)
	}

	// The same bytes as a CV that is not approved yet replace it, as before:
	// there is no approval to lose.
	fake.listedCVUnapproved.Store(true)
	if res, text = call(t, cs, "upload_cv", map[string]any{"path": "approved.pdf"}); res.IsError {
		t.Fatalf("uploading an unapproved CV's file again should go through: %s", text)
	}
	if n := uploads(fake); n != 1 {
		t.Fatalf("want one upload, got %d", n)
	}

	// Without the list there is no telling, so nothing is sent.
	fake.cvListFails.Store(true)
	if res, text = call(t, cs, "upload_cv", map[string]any{"path": "cv.pdf"}); !res.IsError || !strings.Contains(text, "nothing was uploaded") {
		t.Fatalf("upload_cv should refuse when the CVs cannot be listed: %v: %s", res.IsError, text)
	}
	if n := uploads(fake); n != 1 {
		t.Fatalf("an upload went out without the check: %d uploads", n)
	}
}

func TestUploadCVPassesTheAPIsRefusalThrough(t *testing.T) {
	allowed, _ := cvFolders(t)
	if err := os.WriteFile(filepath.Join(allowed, "scanned.pdf"), fakePDF, 0o600); err != nil {
		t.Fatal(err)
	}
	cs, _ := connectOptions(t, Options{Token: "test-token", CVDir: allowed}, nil, nil, false, true)
	res, text := call(t, cs, "upload_cv", map[string]any{"path": "scanned.pdf"})
	if !res.IsError || !strings.Contains(text, "scanned image") {
		t.Fatalf("the API's own refusal should reach the host, got %v: %s", res.IsError, text)
	}
}
