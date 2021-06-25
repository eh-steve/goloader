// +build go1.12
// +build !go1.17

package goloader

import (
	"unsafe"
)

// A stackObjectRecord is generated by the compiler for each stack object in a stack frame.
// This record must match the generator code in cmd/compile/internal/gc/ssa.go:emitStackObjects.
type stackObjectRecord struct {
	// offset in frame
	// if negative, offset from varp
	// if non-negative, offset from argp
	off int
	typ *_type
}

func setStackObjectPtr(obj *stackObjectRecord, ptr unsafe.Pointer) {
	obj.typ = (*_type)(ptr)
}
