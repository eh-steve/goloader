package goloader

import (
	"cmd/objfile/objabi"
	"encoding/binary"
	"fmt"
	"github.com/pkujhd/goloader/obj"
	"github.com/pkujhd/goloader/objabi/reloctype"
	"github.com/pkujhd/goloader/objabi/symkind"
	"github.com/pkujhd/goloader/objabi/tls"
	"strings"
)

func (linker *Linker) relocateADRP(mCode []byte, loc obj.Reloc, segment *segment, symAddr uintptr) {
	byteorder := linker.Arch.ByteOrder
	signedOffset := int64(symAddr) + int64(loc.Add) - ((int64(segment.codeBase) + int64(loc.Offset)) &^ 0xFFF)
	if oldMcode, ok := linker.appliedADRPRelocs[&mCode[0]]; !ok {
		linker.appliedADRPRelocs[&mCode[0]] = make([]byte, 8)
		copy(linker.appliedADRPRelocs[&mCode[0]], mCode)
	} else {
		copy(mCode, oldMcode)
	}
	// R_ADDRARM64 relocs include 2x 32 bit instructions, one ADRP, and one ADD - both contain the destination register in the lowest 5 bits
	if signedOffset > 1<<32 || signedOffset < -1<<32 {
		// Too far to fit inside an ADRP+ADD, do a jump to some extra code we add at the end big enough to fit any 64 bit address
		symAddr += uintptr(loc.Add)
		addr := byteorder.Uint32(mCode)
		bcode := byteorder.Uint32(arm64Bcode) // Unconditional branch
		bcode |= ((uint32(segment.codeOff) - uint32(loc.Offset)) >> 2) & 0x01FFFFFF
		if segment.codeOff-loc.Offset < 0 {
			bcode |= 0x02000000 // 26th bit is sign bit
		}
		byteorder.PutUint32(mCode, bcode) // The second ADD instruction in the ADRP reloc will be bypassed as we return from the jump after it
		//low: MOV reg imm
		llow := uint32(0xD2800000)
		//lhigh: MOVK reg imm LSL#16
		lhigh := uint32(0xF2A00000)
		//llow: MOVK reg imm LSL#32
		hlow := uint32(0xF2C00000)
		//lhigh: MOVK reg imm LSL#48
		hhigh := uint32(0xF2E00000)
		llow = ((addr & 0x1F) | llow) | ((uint32(symAddr) & 0xFFFF) << 5)
		lhigh = ((addr & 0x1F) | lhigh) | (uint32(symAddr) >> 16 << 5)
		putAddressAddOffset(byteorder, segment.codeByte, &segment.codeOff, uint64(llow)|(uint64(lhigh)<<32))
		hlow = ((addr & 0x1F) | hlow) | uint32(((uint64(symAddr)>>32)&0xFFFF)<<5)
		hhigh = ((addr & 0x1F) | hhigh) | uint32((uint64(symAddr)>>48)<<5)
		putAddressAddOffset(byteorder, segment.codeByte, &segment.codeOff, uint64(hlow)|(uint64(hhigh)<<32))
		bcode = byteorder.Uint32(arm64Bcode)
		bcode |= ((uint32(loc.Offset) - uint32(segment.codeOff) + 8) >> 2) & 0x01FFFFFF
		if loc.Offset-segment.codeOff+8 < 0 {
			bcode |= 0x02000000
		}
		byteorder.PutUint32(segment.codeByte[segment.codeOff:], bcode)
		segment.codeOff += Uint32Size
	} else {
		// Bit layout of ADRP instruction is:

		// 31  30  29  28  27  26  25  24  23  22  21  20  19  18  17  16  15  14  13  11  10  09  08  07  06  05  04  03  02  01  00
		// op  [imlo]   1   0   0   0   0  [<----------------------------- imm hi ----------------------------->]  [  dst register  ]

		// Bit layout of ADD instruction (64-bit) is:

		// 31  30  29  28  27  26  25  24  23  22  21  20  19  18  17  16  15  14  13  11  10  09  08  07  06  05  04  03  02  01  00
		//  1   0   0   1   0   0   0   1   0   0  [<--------------- imm12 ---------------->]  [  src register  ]  [  dst register  ]
		// sf <- 64 bit                        sh <- whether to left shift imm12 by 12 bits

		immLow := uint32((uint64(signedOffset)>>12)&3) << 29
		immHigh := uint32((uint64(signedOffset)>>12>>2)&0x7FFFF) << 5
		adrp := byteorder.Uint32(mCode[0:4])
		adrp |= immLow | immHigh
		add := byteorder.Uint32(mCode[4:8])
		add |= uint32(uint64(signedOffset)&0xFFF) << 10
		byteorder.PutUint32(mCode, adrp)
		byteorder.PutUint32(mCode[4:], add)
	}
}

func (linker *Linker) relocateCALL(addr uintptr, loc obj.Reloc, segment *segment, relocByte []byte, addrBase int) {
	byteorder := linker.Arch.ByteOrder
	offset := int(addr) - (addrBase + loc.Offset + loc.Size) + loc.Add
	if offset > 0x7FFFFFFF || offset < -0x80000000 {
		offset = (segment.codeBase + segment.codeOff) - (addrBase + loc.Offset + loc.Size)
		copy(segment.codeByte[segment.codeOff:], x86amd64JMPLcode)
		segment.codeOff += len(x86amd64JMPLcode)
		putAddressAddOffset(byteorder, segment.codeByte, &segment.codeOff, uint64(addr)+uint64(loc.Add))
	}
	byteorder.PutUint32(relocByte[loc.Offset:], uint32(offset))
}

func (linker *Linker) relocatePCREL(addr uintptr, loc obj.Reloc, segment *segment, relocByte []byte, addrBase int) (err error) {
	byteorder := linker.Arch.ByteOrder
	offset := int(addr) - (addrBase + loc.Offset + loc.Size) + loc.Add
	if offset > 0x7FFFFFFF || offset < -0x80000000 {
		offset = (segment.codeBase + segment.codeOff) - (addrBase + loc.Offset + loc.Size)
		bytes := relocByte[loc.Offset-2:]
		opcode := relocByte[loc.Offset-2]
		regsiter := ZeroByte
		if opcode == x86amd64LEAcode {
			bytes[0] = x86amd64MOVcode
		} else if opcode == x86amd64MOVcode && loc.Size >= Uint32Size {
			regsiter = ((relocByte[loc.Offset-1] >> 3) & 0x7) | 0xb8
			copy(bytes, x86amd64JMPLcode)
		} else if opcode == x86amd64CMPLcode && loc.Size >= Uint32Size {
			copy(bytes, x86amd64JMPLcode)
		} else if (bytes[1] == x86amd64CALLcode) && binary.LittleEndian.Uint32(relocByte[loc.Offset:]) == 0 {
			// Maybe a CGo call
			copy(bytes, x86amd64JMPNearCode)
			opcode = bytes[1]
			byteorder.PutUint32(bytes[1:], uint32(offset))
		} else if bytes[1] == x86amd64JMPcode && offset < 1<<32 {
			byteorder.PutUint32(bytes[1:], uint32(offset))
		} else {
			return fmt.Errorf("do not support x86 opcode: %x for symbol %s (offset %d)!\n", relocByte[loc.Offset-2:loc.Offset], loc.Sym.Name, offset)
		}
		byteorder.PutUint32(relocByte[loc.Offset:], uint32(offset))
		if opcode == x86amd64CMPLcode || opcode == x86amd64MOVcode {
			putAddressAddOffset(byteorder, segment.codeByte, &segment.codeOff, uint64(segment.codeBase+segment.codeOff+PtrSize))
			if opcode == x86amd64CMPLcode {
				copy(segment.codeByte[segment.codeOff:], x86amd64replaceCMPLcode)
				segment.codeByte[segment.codeOff+0x0F] = relocByte[loc.Offset+loc.Size]
				segment.codeOff += len(x86amd64replaceCMPLcode)
				putAddressAddOffset(byteorder, segment.codeByte, &segment.codeOff, uint64(addr))
			} else {
				copy(segment.codeByte[segment.codeOff:], x86amd64replaceMOVQcode)
				segment.codeByte[segment.codeOff+1] = regsiter
				copy2Slice(segment.codeByte[segment.codeOff+2:], addr, PtrSize)
				segment.codeOff += len(x86amd64replaceMOVQcode)
			}
			putAddressAddOffset(byteorder, segment.codeByte, &segment.codeOff, uint64(addrBase+loc.Offset+loc.Size-loc.Add))
		} else if opcode == x86amd64CALLcode {
			copy(segment.codeByte[segment.codeOff:], x86amd64replaceCALLcode)
			byteorder.PutUint64(segment.codeByte[segment.codeOff+4:], uint64(addr))
			segment.codeOff += len(x86amd64replaceCALLcode)
			copy(segment.codeByte[segment.codeOff:], x86amd64JMPNearCode)
			byteorder.PutUint32(segment.codeByte[segment.codeOff+1:], uint32(offset))
			segment.codeOff += len(x86amd64JMPNearCode)
		} else {
			putAddressAddOffset(byteorder, segment.codeByte, &segment.codeOff, uint64(addr))
		}
	} else {
		byteorder.PutUint32(relocByte[loc.Offset:], uint32(offset))
	}
	return err
}

func (linker *Linker) relocateCALLARM(addr uintptr, loc obj.Reloc, segment *segment) {
	byteorder := linker.Arch.ByteOrder
	add := loc.Add
	if loc.Type == reloctype.R_CALLARM {
		add = int(signext24(int64(loc.Add&0xFFFFFF)) * 4)
	}
	offset := (int(addr) + add - (segment.codeBase + loc.Offset)) / 4
	if offset > 0x7FFFFF || offset < -0x800000 {
		segment.codeOff = alignof(segment.codeOff, PtrSize)
		off := uint32(segment.codeOff-loc.Offset) / 4
		if loc.Type == reloctype.R_CALLARM {
			add = int(signext24(int64(loc.Add&0xFFFFFF)+2) * 4)
			off = uint32(segment.codeOff-loc.Offset-8) / 4
		}
		putUint24(segment.codeByte[loc.Offset:], off)
		if loc.Type == reloctype.R_CALLARM64 {
			copy(segment.codeByte[segment.codeOff:], arm64code)
			segment.codeOff += len(arm64code)
		} else {
			copy(segment.codeByte[segment.codeOff:], armcode)
			segment.codeOff += len(armcode)
		}
		putAddressAddOffset(byteorder, segment.codeByte, &segment.codeOff, uint64(int(addr)+add))
	} else {
		val := byteorder.Uint32(segment.codeByte[loc.Offset:])
		if loc.Type == reloctype.R_CALLARM {
			val |= uint32(offset) & 0x00FFFFFF
		} else {
			val |= uint32(offset) & 0x03FFFFFF
		}
		byteorder.PutUint32(segment.codeByte[loc.Offset:], val)
	}
}

func (linker *Linker) relocate(codeModule *CodeModule, symbolMap map[string]uintptr) (err error) {
	segment := &codeModule.segment
	byteorder := linker.Arch.ByteOrder

	for _, symbol := range linker.symMap {
		for _, loc := range symbol.Reloc {
			addr := symbolMap[loc.Sym.Name]
			fmAddr, duplicated := symbolMap[FirstModulePrefix+loc.Sym.Name]
			if duplicated {
				addr = fmAddr
			}
			sym := loc.Sym
			relocByte := segment.dataByte
			addrBase := segment.dataBase
			if symbol.Kind == symkind.STEXT {
				addrBase = segment.codeBase
				relocByte = segment.codeByte
			}
			if addr == 0 && strings.HasPrefix(sym.Name, ItabPrefix) {
				addr = uintptr(segment.dataBase + loc.Sym.Offset)
				symbolMap[loc.Sym.Name] = addr
				codeModule.module.itablinks = append(codeModule.module.itablinks, (*itab)(adduintptr(uintptr(segment.dataBase), loc.Sym.Offset)))
			}

			if addr != InvalidHandleValue {
				switch loc.Type {
				case reloctype.R_TLS_LE:
					if _, ok := symbolMap[TLSNAME]; !ok {
						symbolMap[TLSNAME] = tls.GetTLSOffset(linker.Arch, PtrSize)
					}
					byteorder.PutUint32(relocByte[loc.Offset:], uint32(symbolMap[TLSNAME]))
				case reloctype.R_CALL:
					linker.relocateCALL(addr, loc, segment, relocByte, addrBase)
				case reloctype.R_PCREL:
					err = linker.relocatePCREL(addr, loc, segment, relocByte, addrBase)
				case reloctype.R_CALLARM, reloctype.R_CALLARM64:
					linker.relocateCALLARM(addr, loc, segment)
				case reloctype.R_ADDRARM64:
					if symbol.Kind != symkind.STEXT {
						err = fmt.Errorf("impossible!Sym:%s locate not in code segment!\n", sym.Name)
					}
					linker.relocateADRP(relocByte[loc.Offset:], loc, segment, addr)
				case reloctype.R_ADDR, reloctype.R_WEAKADDR:
					address := uintptr(int(addr) + loc.Add)
					putAddress(byteorder, relocByte[loc.Offset:], uint64(address))
				case reloctype.R_CALLIND:
					//nothing todo
				case reloctype.R_ADDROFF, reloctype.R_WEAKADDROFF:
					offset := int(addr) - addrBase + loc.Add
					if offset > 0x7FFFFFFF || offset < -0x80000000 {
						err = fmt.Errorf("symName:%s offset:%d is overflow!\n", sym.Name, offset)
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
				case reloctype.R_USETYPE:
					//nothing todo
				case reloctype.R_USEIFACE:
					//nothing todo
				case reloctype.R_USEIFACEMETHOD:
					//nothing todo
				case reloctype.R_ADDRCUOFF:
					//nothing todo
				case reloctype.R_KEEP:
					//nothing todo
				default:
					err = fmt.Errorf("unknown reloc type: %s sym: %s", objabi.RelocType(loc.Type).String(), sym.Name)
				}
			}
			if err != nil {
				return err
			}
		}
	}
	return err
}
