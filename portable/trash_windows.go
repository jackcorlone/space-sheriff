//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	foDelete     = 3
	fofSilent    = 0x0004
	fofNoConfirm = 0x0010
	fofAllowUndo = 0x0040
	fofNoErrorUI = 0x0400
)

var shFileOperation = syscall.NewLazyDLL("shell32.dll").NewProc("SHFileOperationW")

type shFileOp struct {
	hwnd              uintptr
	function          uint32
	from              *uint16
	to                *uint16
	flags             uint16
	operationsAborted int32
	nameMappings      uintptr
	progressTitle     *uint16
}

func moveToTrash(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("当前版本只允许清理普通文件")
	}
	from, err := syscall.UTF16FromString(path)
	if err != nil {
		return err
	}
	from = append(from, 0)
	operation := shFileOp{
		function: foDelete,
		from:     &from[0],
		flags:    fofSilent | fofNoConfirm | fofAllowUndo | fofNoErrorUI,
	}
	result, _, _ := shFileOperation.Call(uintptr(unsafe.Pointer(&operation)))
	if result != 0 || operation.operationsAborted != 0 {
		return fmt.Errorf("Windows 回收站操作失败（代码 %d）", result)
	}
	return nil
}
