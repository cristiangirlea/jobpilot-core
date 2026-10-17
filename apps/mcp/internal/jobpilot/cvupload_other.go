//go:build !unix

package jobpilot

// openNonBlocking is unix-only; Windows has no named pipes in a folder.
const openNonBlocking = 0
