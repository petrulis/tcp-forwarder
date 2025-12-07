package main

import (
	"errors"
	"syscall"
)

// isRecoverable tries to replace ne.Temporary() which was deprecated a long time ago
// but no clear replacement was provided.
// See https://github.com/golang/go/issues/45729 and https://groups.google.com/g/golang-nuts/c/-JcZzOkyqYI/m/xwaZzjCgAwAJ
// for context.
func isRecoverable(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.EMFILE, syscall.ENFILE:
			return true // Too many open files - might recover?
		case syscall.ECONNABORTED:
			return true
		}
	}
	return false
}
