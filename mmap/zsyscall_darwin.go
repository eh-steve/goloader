//go:build darwin
// +build darwin

package mmap

//go:linkname syscall6X runtime.syscall6X
func syscall6X(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno)

//go:linkname syscall runtime.syscall
func syscall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno)

func mmap(addr uintptr, length uintptr, prot int, flag int, fd int, pos int64) (ret uintptr, err error) {
	r0, _, e1 := syscall6X(abi.FuncPCABI0(libc_mmap_trampoline), uintptr(addr), uintptr(length), uintptr(prot), uintptr(flag), uintptr(fd), uintptr(pos))
	ret = uintptr(r0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func libc_mmap_trampoline()

//go:cgo_import_dynamic libc_mmap mmap "/usr/lib/libSystem.B.dylib"

func munmap(addr uintptr, length uintptr) (err error) {
	_, _, e1 := syscall(abi.FuncPCABI0(libc_munmap_trampoline), uintptr(addr), uintptr(length), 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func libc_munmap_trampoline()

//go:cgo_import_dynamic libc_munmap munmap "/usr/lib/libSystem.B.dylib"
