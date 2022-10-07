//go:build go1.16 && !go1.20
// +build go1.16,!go1.20

package obj

import (
	"cmd/objfile/archive"
	"cmd/objfile/goobj"
	"cmd/objfile/objabi"
	"fmt"
	"strings"
)

func (pkg *Pkg) Symbols() error {
	a, err := archive.Parse(pkg.F, false)
	if err != nil {
		return err
	}

	for _, e := range a.Entries {
		switch e.Type {
		case archive.EntryPkgDef:
			//nothing todo
		case archive.EntryGoObj:
			b := make([]byte, e.Obj.Size)
			_, err := pkg.F.ReadAt(b, e.Obj.Offset)
			if err != nil {
				return err
			}
			r := goobj.NewReaderFromBytes(b, false)
			// Name of referenced indexed symbols.
			nrefName := r.NRefName()
			refNames := make(map[goobj.SymRef]string, nrefName)
			for i := 0; i < nrefName; i++ {
				rn := r.RefName(i)
				refNames[rn.Sym()] = rn.Name(r)
			}
			pkg.Arch = e.Obj.Arch
			nsym := r.NSym() + r.NHashed64def() + r.NHasheddef() + r.NNonpkgdef()
			for i := 0; i < nsym; i++ {
				pkg.addSym(r, uint32(i), &refNames)
			}
			files := make([]string, r.NFile())
			for i := range files {
				files[i] = r.File(i)
			}

			pkg.CUFiles = append(pkg.CUFiles, CompilationUnitFiles{
				ArchiveName: e.Name,
				Files:       files,
			})
		default:
			return fmt.Errorf("Parse open %s: unrecognized archive member %s\n", pkg.F.Name(), e.Name)
		}
	}
	for _, sym := range pkg.Syms {
		if !strings.HasPrefix(sym.Name, TypeStringPrefix) {
			sym.Name = strings.Replace(sym.Name, EmptyPkgPath, pkg.PkgPath, -1)
		}
	}
	return nil
}

func resolveSymRef(s goobj.SymRef, r *goobj.Reader, refNames *map[goobj.SymRef]string) (string, uint32) {
	i := InvalidIndex
	switch p := s.PkgIdx; p {
	case goobj.PkgIdxInvalid:
		if s.SymIdx != 0 {
			panic("bad sym ref")
		}
		return EmptyString, i
	case goobj.PkgIdxHashed64:
		i = s.SymIdx + uint32(r.NSym())
	case goobj.PkgIdxHashed:
		i = s.SymIdx + uint32(r.NSym()+r.NHashed64def())
	case goobj.PkgIdxNone:
		i = s.SymIdx + uint32(r.NSym()+r.NHashed64def()+r.NHasheddef())
	case goobj.PkgIdxBuiltin:
		name, _ := goobj.BuiltinName(int(s.SymIdx))
		return name, i
	case goobj.PkgIdxSelf:
		i = s.SymIdx
	default:
		return (*refNames)[s], i
	}
	return r.Sym(i).Name(r), i
}

func (pkg *Pkg) addSym(r *goobj.Reader, idx uint32, refNames *map[goobj.SymRef]string) {
	s := r.Sym(idx)
	symbol := ObjSymbol{Name: s.Name(r), Kind: int(s.Type()), DupOK: s.Dupok(), Size: (int64)(s.Siz()), Func: &FuncInfo{}}
	if _, ok := pkg.Syms[symbol.Name]; ok {
		return
	}
	if objabi.SymKind(symbol.Kind) == objabi.Sxxx || symbol.Name == EmptyString {
		return
	}
	if objabi.SymKind(symbol.Kind) == objabi.Sxxx || symbol.Name == EmptyString {
		return
	}
	if symbol.Size > 0 {
		symbol.Data = r.Data(idx)
		grow(&symbol.Data, (int)(symbol.Size))
	} else {
		symbol.Data = make([]byte, 0)
	}

	pkg.Syms[symbol.Name] = &symbol

	auxs := r.Auxs(idx)
	for k := 0; k < len(auxs); k++ {
		name, index := resolveSymRef(auxs[k].Sym(), r, refNames)
		switch auxs[k].Type() {
		case goobj.AuxGotype:
			symbol.Type = name
		case goobj.AuxFuncInfo:
			funcInfo := goobj.FuncInfo{}
			readFuncInfo(&funcInfo, r.Data(index), symbol.Func)
			for _, index := range funcInfo.File {
				symbol.Func.File = append(symbol.Func.File, r.File(int(index)))
			}
			cuOffset := 0
			for _, cuFiles := range pkg.CUFiles {
				cuOffset += len(cuFiles.Files)
			}
			symbol.Func.CUOffset = cuOffset
			for _, inl := range funcInfo.InlTree {
				funcname, _ := resolveSymRef(inl.Func, r, refNames)
				funcname = strings.Replace(funcname, EmptyPkgPath, pkg.PkgPath, -1)
				inlNode := InlTreeNode{
					Parent:   int64(inl.Parent),
					File:     r.File(int(inl.File)),
					Line:     int64(inl.Line),
					Func:     funcname,
					ParentPC: int64(inl.ParentPC),
				}
				symbol.Func.InlTree = append(symbol.Func.InlTree, inlNode)
			}
		case goobj.AuxFuncdata:
			symbol.Func.FuncData = append(symbol.Func.FuncData, name)
		case goobj.AuxDwarfInfo:
		case goobj.AuxDwarfLoc:
		case goobj.AuxDwarfRanges:
		case goobj.AuxDwarfLines:
		case goobj.AuxPcsp:
			symbol.Func.PCSP = r.Data(index)
		case goobj.AuxPcfile:
			symbol.Func.PCFile = r.Data(index)
		case goobj.AuxPcline:
			symbol.Func.PCLine = r.Data(index)
		case goobj.AuxPcinline:
			symbol.Func.PCInline = r.Data(index)
		case goobj.AuxPcdata:
			symbol.Func.PCData = append(symbol.Func.PCData, r.Data(index))
		}
		if _, ok := pkg.Syms[name]; !ok && index != InvalidIndex {
			pkg.addSym(r, index, refNames)
		}
	}

	relocs := r.Relocs(idx)
	for k := 0; k < len(relocs); k++ {
		symbol.Reloc = append(symbol.Reloc, Reloc{})
		symbol.Reloc[k].Add = int(relocs[k].Add())
		symbol.Reloc[k].Offset = int(relocs[k].Off())
		symbol.Reloc[k].Size = int(relocs[k].Siz())
		symbol.Reloc[k].Type = int(relocs[k].Type())
		name, index := resolveSymRef(relocs[k].Sym(), r, refNames)
		symbol.Reloc[k].Sym = &Sym{Name: name, Offset: InvalidOffset}
		if _, ok := pkg.Syms[name]; !ok && index != InvalidIndex {
			pkg.addSym(r, index, refNames)
		}
	}
}
