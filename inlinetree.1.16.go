//go:build go1.16 && !go1.19
// +build go1.16,!go1.19

package goloader

// inlinedCall is the encoding of entries in the FUNCDATA_InlTree table.
type inlinedCall struct {
	parent   int16  // index of parent in the inltree, or < 0
	funcID   funcID // type of the called function
	_        byte
	file     int32 // fileno index into filetab
	line     int32 // line number of the call site
	func_    int32 // offset into pclntab for name of called function
	parentPc int32 // position of an instruction whose source position is the call site (offset from entry)
}

func (linker *Linker) initInlinedCall(inl InlTreeNode, _func *_func) inlinedCall {
	inlname := inl.Func
	return inlinedCall{
		parent:   int16(inl.Parent),
		funcID:   _func.funcID,
		file:     findFileTab(linker, inl.File),
		line:     int32(inl.Line),
		func_:    int32(linker.namemap[inlname]),
		parentPc: int32(inl.ParentPC)}
}

func (linker *Linker) addInlineTree(_func *_func, objsym *ObjSymbol) (err error) {
	return linker._addInlineTree(_func, objsym)
}
