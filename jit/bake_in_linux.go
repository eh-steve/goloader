//go:build linux
// +build linux

package jit

import (
	"reflect"
	"syscall"
)

func bakeInPlatform() {
	_ = reflect.TypeOf(reflect.ValueOf(syscall.Setuid))
	_ = reflect.TypeOf(reflect.ValueOf(syscall.AllThreadsSyscall))
}