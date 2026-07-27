//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

func diskUsage(path string) (total, free uint64, err error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var available uint64
	result, _, callErr := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&available)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&free)),
	)
	if result == 0 {
		return 0, 0, callErr
	}
	return total, free, nil
}
