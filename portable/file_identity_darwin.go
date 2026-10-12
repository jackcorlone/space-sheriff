package main

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(_ string, info os.FileInfo) (string, int64, uint64) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", info.Size(), 1
	}
	return fmt.Sprintf("%x:%x", uint32(stat.Dev), stat.Ino), stat.Blocks * 512, uint64(stat.Nlink)
}
