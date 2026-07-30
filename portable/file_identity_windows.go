package main

import (
	"fmt"
	"os"
	"syscall"
)

const fileReadAttributes = 0x80

func fileIdentity(path string, info os.FileInfo) (string, int64, uint64) {
	pathUTF16, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", info.Size(), 1
	}
	handle, err := syscall.CreateFile(
		pathUTF16,
		fileReadAttributes,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return "", info.Size(), 1
	}
	defer syscall.CloseHandle(handle)
	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &data); err != nil {
		return "", info.Size(), 1
	}
	identity := fmt.Sprintf(
		"%x:%08x%08x",
		data.VolumeSerialNumber,
		data.FileIndexHigh,
		data.FileIndexLow,
	)
	return identity, info.Size(), uint64(data.NumberOfLinks)
}
