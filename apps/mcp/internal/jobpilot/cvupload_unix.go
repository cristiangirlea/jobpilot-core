//go:build unix

package jobpilot

import "syscall"

// openNonBlocking keeps the open itself from waiting on a named pipe swapped
// in after the path was checked; it changes nothing for a regular file.
const openNonBlocking = syscall.O_NONBLOCK
