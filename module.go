package goloader

import (
	"encoding/binary"
	"errors"
	"strings"
	"unsafe"
)

//go:linkname firstmoduledata runtime.firstmoduledata
var firstmoduledata moduledata

type functab struct {
	entry   uintptr
	funcoff uintptr
}

// findfunctab is an array of these structures.
// Each bucket represents 4096 bytes of the text segment.
// Each subbucket represents 256 bytes of the text segment.
// To find a function given a pc, locate the bucket and subbucket for
// that pc. Add together the idx and subbucket value to obtain a
// function index. Then scan the functab array starting at that
// index to find the target function.
// This table uses 20 bytes for every 4096 bytes of code, or ~0.5% overhead.
type findfuncbucket struct {
	idx        uint32
	subbuckets [16]byte
}

// Mapping information for secondary text sections
type textsect struct {
	vaddr    uintptr // prelinked section vaddr
	length   uintptr // section length
	baseaddr uintptr // relocated section address
}

type nameOff int32
type typeOff int32
type textOff int32

// A ptabEntry is generated by the compiler for each exported function
// and global variable in the main package of a plugin. It is used to
// initialize the plugin module's symbol map.
type ptabEntry struct {
	name nameOff
	typ  typeOff
}

type modulehash struct {
	modulename   string
	linktimehash string
	runtimehash  *string
}

type bitvector struct {
	n        int32 // # of bits
	bytedata *uint8
}

type stackmap struct {
	n        int32   // number of bitmaps
	nbit     int32   // number of bits in each bitmap
	bytedata [1]byte // bitmaps, each starting on a byte boundary
}

type funcInfo struct {
	*_func
	datap *moduledata
}

const minfunc = 16                 // minimum function size
const pcbucketsize = 256 * minfunc // size of bucket in the pc->func lookup table
const nsub = len(findfuncbucket{}.subbuckets)

//go:linkname step runtime.step
func step(p []byte, pc *uintptr, val *int32, first bool) (newp []byte, ok bool)

//go:linkname findfunc runtime.findfunc
func findfunc(pc uintptr) funcInfo

//go:linkname funcdata runtime.funcdata
func funcdata(f funcInfo, i int32) unsafe.Pointer

//go:linkname funcname runtime.funcname
func funcname(f funcInfo) string

//go:linkname gostringnocopy runtime.gostringnocopy
func gostringnocopy(str *byte) string

//go:linkname moduledataverify1 runtime.moduledataverify1
func moduledataverify1(datap *moduledata)

func readFuncData(codeReloc *CodeReloc, objsym objSym, objSymMap map[string]objSym, codeLen int) (err error) {
	fd := readAtSeeker{ReadSeeker: objsym.file}
	symbol := objsym.sym

	x := codeLen
	b := x / pcbucketsize
	i := x % pcbucketsize / (pcbucketsize / nsub)
	for lb := b - len(codeReloc.pcfunc); lb >= 0; lb-- {
		codeReloc.pcfunc = append(codeReloc.pcfunc, findfuncbucket{
			idx: uint32(256 * len(codeReloc.pcfunc))})
	}
	bucket := &codeReloc.pcfunc[b]
	bucket.subbuckets[i] = byte(len(codeReloc._func) - int(bucket.idx))

	pcFileHead := make([]byte, 32)
	pcFileHeadSize := binary.PutUvarint(pcFileHead, uint64(len(codeReloc.filetab))<<1)
	for _, fileName := range symbol.Func.File {
		if offset, ok := codeReloc.namemap[fileName]; !ok {
			codeReloc.filetab = append(codeReloc.filetab, (uint32)(len(codeReloc.pclntable)))
			codeReloc.namemap[fileName] = len(codeReloc.pclntable)
			fileName = strings.TrimLeft(fileName, FILE_SYM_PREFIX)
			codeReloc.pclntable = append(codeReloc.pclntable, []byte(fileName)...)
			codeReloc.pclntable = append(codeReloc.pclntable, ZERO_BYTE)
		} else {
			codeReloc.filetab = append(codeReloc.filetab, uint32(offset))
		}
	}

	nameOff := len(codeReloc.pclntable)
	if offset, ok := codeReloc.namemap[symbol.Name]; !ok {
		codeReloc.namemap[symbol.Name] = len(codeReloc.pclntable)
		codeReloc.pclntable = append(codeReloc.pclntable, []byte(symbol.Name)...)
		codeReloc.pclntable = append(codeReloc.pclntable, ZERO_BYTE)
	} else {
		nameOff = offset
	}

	pcspOff := len(codeReloc.pclntable)
	fd.ReadAtWithSize(&(codeReloc.pclntable), symbol.Func.PCSP.Size, symbol.Func.PCSP.Offset)

	pcfileOff := len(codeReloc.pclntable)
	codeReloc.pclntable = append(codeReloc.pclntable, pcFileHead[:pcFileHeadSize-1]...)
	fd.ReadAtWithSize(&(codeReloc.pclntable), symbol.Func.PCFile.Size, symbol.Func.PCFile.Offset)

	pclnOff := len(codeReloc.pclntable)
	fd.ReadAtWithSize(&(codeReloc.pclntable), symbol.Func.PCLine.Size, symbol.Func.PCLine.Offset)

	_func := init_func(symbol, nameOff, pcspOff, pcfileOff, pclnOff)
	for _, data := range symbol.Func.PCData {
		fd.ReadAtWithSize(&(codeReloc.pclntable), data.Size, data.Offset)
	}

	readPCInline(codeReloc, symbol, &fd)

	for _, data := range symbol.Func.FuncData {
		if _, ok := codeReloc.stkmaps[data.Sym.Name]; !ok {
			if gcobj, ok := objSymMap[data.Sym.Name]; ok {
				codeReloc.stkmaps[data.Sym.Name] = make([]byte, gcobj.sym.Data.Size)
				fd := readAtSeeker{ReadSeeker: gcobj.file}
				fd.ReadAt(codeReloc.stkmaps[data.Sym.Name], gcobj.sym.Data.Offset)
			} else if len(data.Sym.Name) == 0 {
				codeReloc.stkmaps[data.Sym.Name] = nil
			} else {
				err = errors.New("unknown gcobj:" + data.Sym.Name)
			}
		}
	}
	codeReloc._func = append(codeReloc._func, _func)

	for _, data := range symbol.Func.FuncData {
		if _, ok := objSymMap[data.Sym.Name]; ok {
			relocSym(codeReloc, data.Sym.Name, objSymMap)
		}
	}
	return
}

func addModule(codeModule *CodeModule) {
	modules[codeModule.module] = true
	for datap := &firstmoduledata; ; {
		if datap.next == nil {
			datap.next = codeModule.module
			break
		}
		datap = datap.next
	}
}
func removeModule(module interface{}) {
	prevp := &firstmoduledata
	for datap := &firstmoduledata; datap != nil; {
		if datap == module {
			prevp.next = datap.next
			break
		}
		prevp = datap
		datap = datap.next
	}
	delete(modules, module)
}
