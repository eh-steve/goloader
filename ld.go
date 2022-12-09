package goloader

import (
	"cmd/objfile/objabi"
	"cmd/objfile/sys"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"github.com/pkujhd/goloader/obj"
	"github.com/pkujhd/goloader/objabi/reloctype"
	"github.com/pkujhd/goloader/objabi/symkind"
	"github.com/pkujhd/goloader/stackobject"
)

// ourself defined struct
// code segment
type segment struct {
	codeByte      []byte
	dataByte      []byte
	codeBase      int
	dataBase      int
	sumDataLen    int
	dataLen       int
	noptrdataLen  int
	bssLen        int
	noptrbssLen   int
	codeLen       int
	maxCodeLength int
	maxDataLength int
	codeOff       int
	dataOff       int
}

type Linker struct {
	code              []byte
	data              []byte
	noptrdata         []byte
	bss               []byte
	noptrbss          []byte
	cuFiles           []obj.CompilationUnitFiles
	symMap            map[string]*obj.Sym
	objsymbolMap      map[string]*obj.ObjSymbol
	namemap           map[string]int
	fileNameMap       map[string]int
	cutab             []uint32
	filetab           []byte
	funcnametab       []byte
	functab           []byte
	pctab             []byte
	_func             []*_func
	initFuncs         []string
	symNameOrder      []string
	Arch              *sys.Arch
	options           LinkerOptions
	heapStringMap     map[string]*string
	stringMmap        *stringMmap
	appliedADRPRelocs map[*byte][]byte
}

type CodeModule struct {
	segment
	Syms                  map[string]uintptr
	module                *moduledata
	gcdata                []byte
	gcbss                 []byte
	patchedTypeMethodsIfn map[*_type]map[int]struct{}
	patchedTypeMethodsTfn map[*_type]map[int]struct{}
	heapStrings           map[string]*string
	stringMmap            *stringMmap
}

var (
	modules     = make(map[*CodeModule]bool)
	modulesLock sync.Mutex
)

// initialize Linker
func initLinker(c LinkerOptions) (*Linker, error) {
	linker := &Linker{
		// Pad these tabs out so offsets don't start at 0, which is often used in runtime as a special value for "missing"
		// e.g. runtime/traceback.go and runtime/symtab.go both contain checks like:
		// if f.pcsp == 0 ...
		// and
		// if f.nameoff == 0
		funcnametab:       make([]byte, PtrSize),
		pctab:             make([]byte, PtrSize),
		symMap:            make(map[string]*obj.Sym),
		objsymbolMap:      make(map[string]*obj.ObjSymbol),
		namemap:           make(map[string]int),
		fileNameMap:       make(map[string]int),
		heapStringMap:     make(map[string]*string),
		appliedADRPRelocs: make(map[*byte][]byte),
		options:           c,
	}
	if c.HeapStrings && c.StringContainerSize > 0 {
		return nil, fmt.Errorf("can only use HeapStrings or StringContainerSize, not both")
	}
	if c.StringContainerSize > 0 {
		linker.stringMmap = &stringMmap{}
		var err error
		linker.stringMmap.bytes, err = MmapData(c.StringContainerSize)
		linker.stringMmap.size = c.StringContainerSize
		if err == nil {
			linker.stringMmap.addr = uintptr(unsafe.Pointer(&linker.stringMmap.bytes[0]))
		}
	}
	head := make([]byte, unsafe.Sizeof(pcHeader{}))
	copy(head, obj.ModuleHeadx86)
	linker.functab = append(linker.functab, head...)
	linker.functab[len(obj.ModuleHeadx86)-1] = PtrSize
	return linker, nil
}

func (linker *Linker) addSymbols(symbolNames []string) error {
	//static_tmp is 0, golang compile not allocate memory.
	linker.noptrdata = append(linker.noptrdata, make([]byte, IntSize)...)

	for _, cuFileSet := range linker.cuFiles {
		for _, fileName := range cuFileSet.Files {
			if offset, ok := linker.fileNameMap[fileName]; !ok {
				linker.cutab = append(linker.cutab, (uint32)(len(linker.filetab)))
				linker.fileNameMap[fileName] = len(linker.filetab)
				fileName = strings.TrimPrefix(fileName, FileSymPrefix)
				linker.filetab = append(linker.filetab, []byte(fileName)...)
				linker.filetab = append(linker.filetab, ZeroByte)
			} else {
				linker.cutab = append(linker.cutab, uint32(offset))
			}
		}
	}

	for _, objSymName := range symbolNames {
		objSym := linker.objsymbolMap[objSymName]
		if objSym.Kind == symkind.STEXT && objSym.DupOK == false {
			_, err := linker.addSymbol(objSym.Name)
			if err != nil {
				return err
			}
		} else if objSym.Kind == symkind.STEXT && objSym.DupOK {
			// This might be an asm func ABIWRAPPER. Check if one of its relocs points to itself
			// (the abi0 version of itself, without the .abiinternal suffix)
			isAsmWrapper := false

			if objSym.Func != nil && objSym.Func.FuncID == uint8(objabi.FuncID_wrapper) {
				for _, reloc := range objSym.Reloc {
					if reloc.Sym.Name+obj.ABIInternalSuffix == objSym.Name {
						// Relocation pointing at itself (the ABI0 ASM version)
						isAsmWrapper = true
					}
				}
			}
			if isAsmWrapper {
				// This wrapper's symbol has a suffix of .abiinternal to distinguish it from the abi0 ASM func
				_, err := linker.addSymbol(objSym.Name)
				if err != nil {
					return err
				}
			}
		}
		if objSym.Kind == symkind.SNOPTRDATA || objSym.Kind == symkind.SRODATA {
			_, err := linker.addSymbol(objSym.Name)
			if err != nil {
				return err
			}
		}
	}
	for _, sym := range linker.symMap {
		offset := 0
		switch sym.Kind {
		case symkind.SNOPTRDATA, symkind.SRODATA:
			if (linker.options.HeapStrings || linker.options.StringContainerSize > 0) && strings.HasPrefix(sym.Name, TypeStringPrefix) {
				//nothing todo
			} else {
				offset += len(linker.data)
			}
		case symkind.SBSS:
			offset += len(linker.data) + len(linker.noptrdata)
		case symkind.SNOPTRBSS:
			offset += len(linker.data) + len(linker.noptrdata) + len(linker.bss)
		}
		sym.Offset += offset
		if offset != 0 {
			for index := range sym.Reloc {
				sym.Reloc[index].Offset += offset
			}
		}
	}
	linker.symNameOrder = symbolNames
	return nil
}

func (linker *Linker) SymbolOrder() []string {
	return linker.symNameOrder
}

func (linker *Linker) addSymbol(name string) (symbol *obj.Sym, err error) {
	if symbol, ok := linker.symMap[name]; ok {
		return symbol, nil
	}
	objsym := linker.objsymbolMap[name]
	symbol = &obj.Sym{Name: objsym.Name, Kind: objsym.Kind}
	linker.symMap[symbol.Name] = symbol

	switch symbol.Kind {
	case symkind.STEXT:
		symbol.Offset = len(linker.code)
		linker.code = append(linker.code, objsym.Data...)
		bytearrayAlign(&linker.code, PtrSize)
		symbol.Func = &obj.Func{}
		if err := linker.readFuncData(linker.objsymbolMap[name], symbol.Offset); err != nil {
			return nil, err
		}
	case symkind.SDATA:
		symbol.Offset = len(linker.data)
		linker.data = append(linker.data, objsym.Data...)
		if linker.Arch.Name == "arm64" {
			bytearrayAlign(&linker.data, PtrSize)
		}
	case symkind.SNOPTRDATA, symkind.SRODATA:
		//because golang string assignment is pointer assignment, so store go.string constants
		//in a separate segment and not unload when module unload.
		if linker.options.HeapStrings && strings.HasPrefix(symbol.Name, TypeStringPrefix) {
			data := make([]byte, len(objsym.Data))
			copy(data, objsym.Data)
			stringVal := string(data)
			linker.heapStringMap[symbol.Name] = &stringVal
		} else if linker.options.StringContainerSize > 0 && strings.HasPrefix(symbol.Name, TypeStringPrefix) {
			if linker.stringMmap.index+len(objsym.Data) > linker.stringMmap.size {
				return nil, fmt.Errorf("overflow string container. Got object of length %d but size was %d", len(objsym.Data), linker.stringMmap.size)
			}
			symbol.Offset = linker.stringMmap.index
			copy(linker.stringMmap.bytes[linker.stringMmap.index:], objsym.Data)
			linker.stringMmap.index += len(objsym.Data)
		} else {
			symbol.Offset = len(linker.noptrdata)
			linker.noptrdata = append(linker.noptrdata, objsym.Data...)
			if linker.Arch.Name == "arm64" {
				bytearrayAlign(&linker.noptrdata, PtrSize)
			}
		}
	case symkind.SBSS:
		symbol.Offset = len(linker.bss)
		linker.bss = append(linker.bss, objsym.Data...)
		if linker.Arch.Name == "arm64" {
			bytearrayAlign(&linker.bss, PtrSize)
		}
	case symkind.SNOPTRBSS:
		symbol.Offset = len(linker.noptrbss)
		linker.noptrbss = append(linker.noptrbss, objsym.Data...)
		if linker.Arch.Name == "arm64" {
			bytearrayAlign(&linker.noptrbss, PtrSize)
		}
	default:
		return nil, fmt.Errorf("invalid symbol:%s kind:%d", symbol.Name, symbol.Kind)
	}

	for _, loc := range objsym.Reloc {
		reloc := loc
		reloc.Offset = reloc.Offset + symbol.Offset
		if _, ok := linker.objsymbolMap[reloc.Sym.Name]; ok {
			reloc.Sym, err = linker.addSymbol(reloc.Sym.Name)
			if err != nil {
				return nil, err
			}
			if len(linker.objsymbolMap[reloc.Sym.Name].Data) == 0 && reloc.Size > 0 {
				//static_tmp is 0, golang compile not allocate memory.
				//goloader add IntSize bytes on linker.noptrdata[0]
				if reloc.Size <= IntSize {
					reloc.Sym.Offset = 0
				} else {
					return nil, fmt.Errorf("Symbol: %s size: %d > IntSize: %d\n", reloc.Sym.Name, reloc.Size, IntSize)
				}
			}
		} else {
			if reloc.Type == reloctype.R_TLS_LE {
				reloc.Sym.Name = TLSNAME
				reloc.Sym.Offset = loc.Offset
			}
			if reloc.Type == reloctype.R_CALLIND {
				reloc.Sym.Offset = 0
			}
			_, exist := linker.symMap[reloc.Sym.Name]
			if strings.HasPrefix(reloc.Sym.Name, TypeImportPathPrefix) {
				if exist {
					reloc.Sym = linker.symMap[reloc.Sym.Name]
				} else {
					path := strings.Trim(strings.TrimPrefix(reloc.Sym.Name, TypeImportPathPrefix), ".")
					reloc.Sym.Kind = symkind.SNOPTRDATA
					reloc.Sym.Offset = len(linker.noptrdata)
					//name memory layout
					//name { tagLen(byte), len(uint16), str*}
					nameLen := []byte{0, 0, 0}
					binary.BigEndian.PutUint16(nameLen[1:], uint16(len(path)))
					linker.noptrdata = append(linker.noptrdata, nameLen...)
					linker.noptrdata = append(linker.noptrdata, path...)
					linker.noptrdata = append(linker.noptrdata, ZeroByte)
					if linker.Arch.Name == "arm64" {
						bytearrayAlign(&linker.noptrdata, PtrSize)
					}
				}
			}
			if ispreprocesssymbol(reloc.Sym.Name) {
				bytes := make([]byte, UInt64Size)
				if err := preprocesssymbol(linker.Arch.ByteOrder, reloc.Sym.Name, bytes); err != nil {
					return nil, err
				} else {
					if exist {
						reloc.Sym = linker.symMap[reloc.Sym.Name]
					} else {
						reloc.Sym.Kind = symkind.SNOPTRDATA
						reloc.Sym.Offset = len(linker.noptrdata)
						linker.noptrdata = append(linker.noptrdata, bytes...)
						if linker.Arch.Name == "arm64" {
							bytearrayAlign(&linker.noptrdata, PtrSize)
						}
					}
				}
			}
			if !exist {
				//golang1.8, some function generates more than one (MOVQ (TLS), CX)
				//so when same name symbol in linker.symMap, do not update it
				if reloc.Sym.Name != "" {
					linker.symMap[reloc.Sym.Name] = reloc.Sym
				}
			}
		}
		symbol.Reloc = append(symbol.Reloc, reloc)
	}

	if objsym.Type != EmptyString {
		if _, ok := linker.symMap[objsym.Type]; !ok {
			if _, ok := linker.objsymbolMap[objsym.Type]; !ok {
				linker.symMap[objsym.Type] = &obj.Sym{Name: objsym.Type, Offset: InvalidOffset}
			}
		}
	}
	return symbol, nil
}

func (linker *Linker) readFuncData(symbol *obj.ObjSymbol, codeLen int) (err error) {
	nameOff := len(linker.funcnametab)
	if offset, ok := linker.namemap[symbol.Name]; !ok {
		linker.namemap[symbol.Name] = len(linker.funcnametab)
		linker.funcnametab = append(linker.funcnametab, []byte(symbol.Name)...)
		linker.funcnametab = append(linker.funcnametab, ZeroByte)
	} else {
		nameOff = offset
	}

	pcspOff := len(linker.pctab)
	linker.pctab = append(linker.pctab, symbol.Func.PCSP...)

	pcfileOff := len(linker.pctab)
	linker.pctab = append(linker.pctab, symbol.Func.PCFile...)

	pclnOff := len(linker.pctab)
	linker.pctab = append(linker.pctab, symbol.Func.PCLine...)

	_func := initfunc(symbol, nameOff, pcspOff, pcfileOff, pclnOff, symbol.Func.CUOffset)
	linker._func = append(linker._func, &_func)
	Func := linker.symMap[symbol.Name].Func
	for _, pcdata := range symbol.Func.PCData {
		Func.PCData = append(Func.PCData, uint32(len(linker.pctab)))
		linker.pctab = append(linker.pctab, pcdata...)
	}

	for _, name := range symbol.Func.FuncData {
		if _, ok := linker.symMap[name]; !ok {
			if _, ok := linker.objsymbolMap[name]; ok {
				if _, err = linker.addSymbol(name); err != nil {
					return err
				}
			} else if len(name) == 0 {
				//nothing todo
			} else {
				return errors.New("unknown gcobj:" + name)
			}
		}
		if sym, ok := linker.symMap[name]; ok {
			Func.FuncData = append(Func.FuncData, (uintptr)(sym.Offset))
		} else {
			Func.FuncData = append(Func.FuncData, (uintptr)(0))
		}
	}

	if err = linker.addInlineTree(&_func, symbol); err != nil {
		return err
	}

	grow(&linker.pctab, alignof(len(linker.pctab), PtrSize))
	return
}

func (linker *Linker) addSymbolMap(symPtr map[string]uintptr, codeModule *CodeModule) (symbolMap map[string]uintptr, err error) {
	symbolMap = make(map[string]uintptr)
	segment := &codeModule.segment
	for name, sym := range linker.symMap {
		if sym.Offset == InvalidOffset {
			if ptr, ok := symPtr[sym.Name]; ok {
				symbolMap[name] = ptr
			} else {
				symbolMap[name] = InvalidHandleValue
				return nil, fmt.Errorf("unresolved external symbol: %s", sym.Name)
			}
		} else if sym.Name == TLSNAME {
			//nothing todo
		} else if sym.Kind == symkind.STEXT {
			symbolMap[name] = uintptr(linker.symMap[name].Offset + segment.codeBase)
			codeModule.Syms[sym.Name] = symbolMap[name]
		} else if strings.HasPrefix(sym.Name, ItabPrefix) {
			if ptr, ok := symPtr[sym.Name]; ok {
				symbolMap[name] = ptr
			}
		} else {
			if _, ok := symPtr[name]; !ok {
				if linker.options.HeapStrings && strings.HasPrefix(name, TypeStringPrefix) {
					strPtr := linker.heapStringMap[name]
					if strPtr == nil {
						return nil, fmt.Errorf("impossible! got a nil string for symbol %s", name)
					}
					if len(*strPtr) == 0 {
						// Any address will do, the length is 0, so it should never be read
						symbolMap[name] = uintptr(unsafe.Pointer(linker))
					} else {
						x := (*reflect.StringHeader)(unsafe.Pointer(strPtr))
						symbolMap[name] = x.Data
					}
				} else if linker.options.StringContainerSize > 0 && strings.HasPrefix(name, TypeStringPrefix) {
					symbolMap[name] = uintptr(linker.symMap[name].Offset) + linker.stringMmap.addr
				} else {
					symbolMap[name] = uintptr(linker.symMap[name].Offset + segment.dataBase)
				}
			} else {
				if strings.HasPrefix(name, MainPkgPrefix) || strings.HasPrefix(name, TypePrefix) {
					symbolMap[name] = uintptr(linker.symMap[name].Offset + segment.dataBase)
					if addr, ok := symPtr[name]; ok {
						// Record the presence of a duplicate symbol by adding a prefix
						// Note - this isn't enough to deduplicate types during relocation,
						// as not all firstmodule types will be in symPtr (especially func types)
						symbolMap[FirstModulePrefix+name] = addr
					}
				} else {
					symbolMap[name] = symPtr[name]
				}
			}
		}
	}
	if tlsG, ok := symPtr[TLSNAME]; ok {
		symbolMap[TLSNAME] = tlsG
	}
	codeModule.heapStrings = linker.heapStringMap
	codeModule.stringMmap = linker.stringMmap
	return symbolMap, err
}

func (linker *Linker) addFuncTab(module *moduledata, _func *_func, symbolMap map[string]uintptr) (err error) {
	funcname := gostringnocopy(&linker.funcnametab[_func.nameoff])
	setfuncentry(_func, symbolMap[funcname], module.text)
	Func := linker.symMap[funcname].Func

	if err = stackobject.AddStackObject(funcname, linker.symMap, symbolMap, module.noptrdata); err != nil {
		return err
	}
	if err = linker.addDeferReturn(_func); err != nil {
		return err
	}

	append2Slice(&module.pclntable, uintptr(unsafe.Pointer(_func)), _FuncSize)

	if _func.npcdata > 0 {
		append2Slice(&module.pclntable, uintptr(unsafe.Pointer(&(Func.PCData[0]))), Uint32Size*int(_func.npcdata))
	}

	if _func.nfuncdata > 0 {
		addfuncdata(module, Func, _func)
	}

	return err
}

func (linker *Linker) buildModule(codeModule *CodeModule, symbolMap map[string]uintptr) (err error) {
	segment := &codeModule.segment
	module := codeModule.module
	module.pclntable = append(module.pclntable, linker.functab...)
	module.minpc = uintptr(segment.codeBase)
	module.maxpc = uintptr(segment.codeBase + segment.codeOff)
	module.text = uintptr(segment.codeBase)
	module.etext = module.maxpc
	module.data = uintptr(segment.dataBase)
	module.edata = uintptr(segment.dataBase) + uintptr(segment.dataLen)
	module.noptrdata = module.edata
	module.enoptrdata = module.noptrdata + uintptr(segment.noptrdataLen)
	module.bss = module.enoptrdata
	module.ebss = module.bss + uintptr(segment.bssLen)
	module.noptrbss = module.ebss
	module.enoptrbss = module.noptrbss + uintptr(segment.noptrbssLen)
	module.end = module.enoptrbss
	module.types = module.data
	module.etypes = module.enoptrbss

	module.ftab = append(module.ftab, initfunctab(module.minpc, uintptr(len(module.pclntable)), module.text))
	for index, _func := range linker._func {
		funcname := gostringnocopy(&linker.funcnametab[_func.nameoff])
		module.ftab = append(module.ftab, initfunctab(symbolMap[funcname], uintptr(len(module.pclntable)), module.text))
		if err = linker.addFuncTab(module, linker._func[index], symbolMap); err != nil {
			return err
		}
	}
	module.ftab = append(module.ftab, initfunctab(module.maxpc, uintptr(len(module.pclntable)), module.text))

	//see:^src/cmd/link/internal/ld/pcln.go findfunctab
	funcbucket := []findfuncbucket{}
	for k, _func := range linker._func {
		funcname := gostringnocopy(&linker.funcnametab[_func.nameoff])
		x := linker.symMap[funcname].Offset
		b := x / pcbucketsize
		i := x % pcbucketsize / (pcbucketsize / nsub)
		for lb := b - len(funcbucket); lb >= 0; lb-- {
			funcbucket = append(funcbucket, findfuncbucket{
				idx: uint32(k)})
		}
		if funcbucket[b].subbuckets[i] == 0 && b != 0 && i != 0 {
			if k-int(funcbucket[b].idx) >= pcbucketsize/minfunc {
				return fmt.Errorf("over %d func in one funcbuckets", k-int(funcbucket[b].idx))
			}
			funcbucket[b].subbuckets[i] = byte(k - int(funcbucket[b].idx))
		}
	}
	length := len(funcbucket) * FindFuncBucketSize
	append2Slice(&module.pclntable, uintptr(unsafe.Pointer(&funcbucket[0])), length)
	module.findfunctab = (uintptr)(unsafe.Pointer(&module.pclntable[len(module.pclntable)-length]))

	if err = linker.addgcdata(codeModule, symbolMap); err != nil {
		return err
	}
	for name, addr := range symbolMap {
		if strings.HasPrefix(name, TypePrefix) &&
			!strings.HasPrefix(name, TypeDoubleDotPrefix) &&
			addr >= module.types && addr < module.etypes {
			module.typelinks = append(module.typelinks, int32(addr-module.types))
			module.typemap[typeOff(addr-module.types)] = (*_type)(unsafe.Pointer(addr))
		}
	}
	initmodule(codeModule.module, linker)

	modulesLock.Lock()
	addModule(codeModule)
	modulesLock.Unlock()
	additabs(codeModule.module)
	moduledataverify1(codeModule.module)
	modulesinit()
	typelinksinit() // Deduplicate typelinks across all modules
	return err
}

func (linker *Linker) deduplicateTypeDescriptors(codeModule *CodeModule, symbolMap map[string]uintptr) (err error) {
	// Having called addModule and runtime.modulesinit(), we can now safely use typesEqual()
	// (which depended on the module being in the linked list for safe name resolution of types).
	// This means we can now deduplicate type descriptors in the actual code
	// by relocating their addresses to the equivalent *_type in the main module

	// We need to deduplicate type symbols with the main module according to type hash, since type assertion
	// uses *_type pointer equality and many overlapping or builtin types may be included twice
	// We have to do this after adding the module to the linked list since deduplication
	// depends on symbol resolution across all modules
	typehash := make(map[uint32][]*_type, len(firstmoduledata.typelinks))
	buildModuleTypeHash(activeModules()[0], typehash)

	patchedTypeMethodsIfn := make(map[*_type]map[int]struct{})
	patchedTypeMethodsTfn := make(map[*_type]map[int]struct{})
	segment := &codeModule.segment
	byteorder := linker.Arch.ByteOrder
	for _, symbol := range linker.symMap {
		for _, loc := range symbol.Reloc {
			addr := symbolMap[loc.Sym.Name]
			sym := loc.Sym
			relocByte := segment.dataByte
			addrBase := segment.dataBase
			if symbol.Kind == symkind.STEXT {
				addrBase = segment.codeBase
				relocByte = segment.codeByte
			}
			if addr != InvalidHandleValue && sym.Kind == symkind.SRODATA &&
				strings.HasPrefix(sym.Name, TypePrefix) &&
				!strings.HasPrefix(sym.Name, TypeDoubleDotPrefix) && sym.Offset != -1 {

				// if this is pointing to a type descriptor at an offset inside this binary, we should deduplicate it against
				// already known types from other modules to allow fast type assertion using *_type pointer equality
				t := (*_type)(unsafe.Pointer(addr))
				prevT := (*_type)(unsafe.Pointer(addr))
				for _, candidate := range typehash[t.hash] {
					seen := map[_typePair]struct{}{}
					if typesEqual(t, candidate, seen) {
						t = candidate
						break
					}
				}

				// Only relocate code if the type is a duplicate
				if t != prevT {
					u := t.uncommon()
					prevU := prevT.uncommon()
					err := codeModule.patchTypeMethodOffsets(t, u, prevU, patchedTypeMethodsIfn, patchedTypeMethodsTfn)
					if err != nil {
						return err
					}

					addr = uintptr(unsafe.Pointer(t))
					if linker.options.RelocationDebugWriter != nil {
						var weakness string
						if loc.Type&reloctype.R_WEAK > 0 {
							weakness = "WEAK|"
						}
						relocType := weakness + objabi.RelocType(loc.Type&^reloctype.R_WEAK).String()
						_, _ = fmt.Fprintf(linker.options.RelocationDebugWriter, "DEDUPLICATING   %10s %10s %18s Base: 0x%x Pos: 0x%08x, Addr: 0x%016x AddrFromBase: %12d %s   to    %s\n",
							objabi.SymKind(symbol.Kind), objabi.SymKind(sym.Kind), relocType, addrBase, uintptr(unsafe.Pointer(&relocByte[loc.Offset])),
							addr, int(addr)-addrBase, symbol.Name, sym.Name)
					}
					switch loc.Type {
					case reloctype.R_PCREL:
						// The replaced t from another module will probably yield a massive negative offset, but that's ok as
						// PC-relative addressing is allowed to be negative (even if not very cache friendly)
						offset := int(addr) - (addrBase + loc.Offset + loc.Size) + loc.Add
						if offset > 0x7FFFFFFF || offset < -0x80000000 {
							err = fmt.Errorf("symName: %s offset: %d overflows!\n", sym.Name, offset)
						}
						byteorder.PutUint32(relocByte[loc.Offset:], uint32(offset))
					case reloctype.R_CALLARM, reloctype.R_CALLARM64:
						panic("This should not be possible")
					case reloctype.R_ADDRARM64:
						linker.relocateADRP(relocByte[loc.Offset:], loc, segment, addr)
					case reloctype.R_ADDR, reloctype.R_WEAKADDR:
						// TODO - sanity check this
						address := uintptr(int(addr) + loc.Add)
						putAddress(byteorder, relocByte[loc.Offset:], uint64(address))
					case reloctype.R_ADDROFF, reloctype.R_WEAKADDROFF:
						offset := int(addr) - addrBase + loc.Add
						if offset > 0x7FFFFFFF || offset < -0x80000000 {
							err = fmt.Errorf("symName: %s offset: %d overflows!\n", sym.Name, offset)
						}
						byteorder.PutUint32(relocByte[loc.Offset:], uint32(offset))
					case reloctype.R_METHODOFF:
						if loc.Sym.Kind == symkind.STEXT {
							addrBase = segment.codeBase
						}
						offset := int(addr) - addrBase + loc.Add
						if offset > 0x7FFFFFFF || offset < -0x80000000 {
							err = fmt.Errorf("symName:%s offset:%d is overflow!\n", sym.Name, offset)
						}
						byteorder.PutUint32(relocByte[loc.Offset:], uint32(offset))
					case reloctype.R_USETYPE, reloctype.R_USEIFACE, reloctype.R_USEIFACEMETHOD, reloctype.R_ADDRCUOFF, reloctype.R_KEEP:
						// nothing to do
					default:
						panic(fmt.Sprintf("unhandled reloc %s", objabi.RelocType(loc.Type)))
						// TODO - should we attempt to rewrite other relocations which point at *_types too?
					}
				}
			}
		}
	}
	codeModule.patchedTypeMethodsIfn = patchedTypeMethodsIfn
	codeModule.patchedTypeMethodsTfn = patchedTypeMethodsTfn

	if err != nil {
		return err
	}
	err = patchTypeMethodTextPtrs(uintptr(codeModule.codeBase), codeModule.patchedTypeMethodsIfn, codeModule.patchedTypeMethodsTfn)

	return err
}

func (linker *Linker) UnresolvedExternalSymbols(symbolMap map[string]uintptr) map[string]*obj.Sym {
	symMap := make(map[string]*obj.Sym)
	for symName, sym := range linker.symMap {
		if sym.Offset == InvalidOffset {
			if _, ok := symbolMap[symName]; !ok {
				if _, ok := linker.objsymbolMap[symName]; !ok {
					symMap[symName] = sym
				}
			}
		}
	}
	return symMap
}

func (linker *Linker) UnresolvedExternalSymbolUsers(symbolMap map[string]uintptr) map[string][]string {
	requiredBy := map[string][]string{}
	for symName, sym := range linker.symMap {
		if sym.Offset == InvalidOffset {
			if _, ok := symbolMap[symName]; !ok {
				if _, ok := linker.objsymbolMap[symName]; !ok {
					var requiredBySet = map[string]struct{}{}
					for _, otherSym := range linker.symMap {
						for _, reloc := range otherSym.Reloc {
							if reloc.Sym.Name == symName {
								requiredBySet[otherSym.Name] = struct{}{}
							}
						}
					}
					requiredByList := make([]string, 0, len(requiredBySet))
					for k := range requiredBySet {
						requiredByList = append(requiredByList, k)
					}
					sort.Strings(requiredByList)
					requiredBy[sym.Name] = requiredByList
				}
			}
		}
	}
	return requiredBy
}

func (linker *Linker) UnloadStrings() error {
	linker.heapStringMap = nil
	if linker.stringMmap != nil {
		return Munmap(linker.stringMmap.bytes)
	}
	return nil
}

func Load(linker *Linker, symPtr map[string]uintptr) (codeModule *CodeModule, err error) {
	codeModule = &CodeModule{
		Syms:   make(map[string]uintptr),
		module: &moduledata{typemap: make(map[typeOff]*_type)},
	}
	codeModule.codeLen = len(linker.code)
	codeModule.dataLen = len(linker.data)
	codeModule.noptrdataLen = len(linker.noptrdata)
	codeModule.bssLen = len(linker.bss)
	codeModule.noptrbssLen = len(linker.noptrbss)
	codeModule.sumDataLen = codeModule.dataLen + codeModule.noptrdataLen + codeModule.bssLen + codeModule.noptrbssLen
	codeModule.maxCodeLength = alignof((codeModule.codeLen)*2, PageSize)
	codeModule.maxDataLength = alignof((codeModule.sumDataLen)*2, PageSize)
	codeByte, err := Mmap(codeModule.maxCodeLength)
	if err != nil {
		return nil, err
	}
	dataByte, err := MmapData(codeModule.maxDataLength)
	if err != nil {
		return nil, err
	}

	codeModule.codeByte = codeByte
	codeModule.codeBase = int((*sliceHeader)(unsafe.Pointer(&codeByte)).Data)
	copy(codeModule.codeByte, linker.code)
	codeModule.codeOff = codeModule.codeLen

	codeModule.dataByte = dataByte
	codeModule.dataBase = int((*sliceHeader)(unsafe.Pointer(&dataByte)).Data)
	copy(codeModule.dataByte[codeModule.dataOff:], linker.data)
	codeModule.dataOff = codeModule.dataLen
	copy(codeModule.dataByte[codeModule.dataOff:], linker.noptrdata)
	codeModule.dataOff += codeModule.noptrdataLen
	copy(codeModule.dataByte[codeModule.dataOff:], linker.bss)
	codeModule.dataOff += codeModule.bssLen
	copy(codeModule.dataByte[codeModule.dataOff:], linker.noptrbss)
	codeModule.dataOff += codeModule.noptrbssLen

	var symbolMap map[string]uintptr
	if symbolMap, err = linker.addSymbolMap(symPtr, codeModule); err == nil {
		if err = linker.relocate(codeModule, symbolMap); err == nil {
			if err = linker.buildModule(codeModule, symbolMap); err == nil {
				if err = linker.deduplicateTypeDescriptors(codeModule, symbolMap); err == nil {
					MakeThreadJITCodeExecutable(uintptr(codeModule.codeBase), codeModule.maxCodeLength)
					if err = linker.doInitialize(codeModule, symbolMap); err == nil {
						return codeModule, err
					}
				}
			}
		}
	}
	if err != nil {
		err2 := Munmap(codeByte)
		err3 := Munmap(dataByte)
		if err2 != nil {
			err = fmt.Errorf("failed to munmap (%s) after linker error: %w", err2, err)
		}
		if err3 != nil {
			err = fmt.Errorf("failed to munmap (%s) after linker error: %w", err3, err)
		}
	}
	return nil, err
}

func (cm *CodeModule) Unload() error {
	err := cm.revertPatchedTypeMethods()
	if err != nil {
		return err
	}
	removeitabs(cm.module)
	runtime.GC()
	modulesLock.Lock()
	removeModule(cm)
	modulesLock.Unlock()
	modulesinit()
	err1 := Munmap(cm.codeByte)
	err2 := Munmap(cm.dataByte)
	if err1 != nil {
		return err1
	}
	cm.heapStrings = nil
	return err2
}

func (cm *CodeModule) UnloadStringMap() error {
	if cm.stringMmap != nil {
		return Munmap(cm.stringMmap.bytes)
	}
	runtime.GC()
	return nil
}
