//go:build unix

package jobpilot

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opening a named pipe blocks until something writes to it, so a FIFO named
// like a CV must be refused before it is opened, or the tool call hangs.
func TestReadCVFileRefusesANamedPipeWithoutOpeningIt(t *testing.T) {
	allowed, _ := cvFolders(t)
	if err := syscall.Mkfifo(filepath.Join(allowed, "pipe.pdf"), 0o600); err != nil {
		t.Skipf("cannot create a named pipe here: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := readCVFile(allowed, "pipe.pdf")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("readCVFile(pipe) = %v, want it refused as not a regular file", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readCVFile blocked on a named pipe; it must refuse it before opening")
	}
}

// A pipe swapped in between the check and the open must not hang the open
// either.
func TestOpenInFolderDoesNotBlockOnANamedPipe(t *testing.T) {
	allowed, _ := cvFolders(t)
	if err := syscall.Mkfifo(filepath.Join(allowed, "pipe.pdf"), 0o600); err != nil {
		t.Skipf("cannot create a named pipe here: %v", err)
	}
	folder, err := os.OpenRoot(allowed)
	if err != nil {
		t.Fatal(err)
	}
	defer folder.Close()
	done := make(chan error, 1)
	go func() {
		f, err := openInFolder(folder, "pipe.pdf")
		if err == nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("opening a named pipe blocked; the open must not wait for a writer")
	}
}
