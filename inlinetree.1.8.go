//go:build go1.8 && !go1.9
// +build go1.8,!go1.9

package goloader

import (
	"cmd/objfile/goobj"
)

type inlinedCall struct{}

func initInline(objFunc *goobj.Func, Func *FuncInfo, pkgpath string, fd *readAtSeeker) (err error) {
	return nil
}

func (linker *Linker) addInlineTree(_func *_func, objsym *ObjSymbol) (err error) {
	return nil
}