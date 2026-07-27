//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func moveToTrash(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("当前版本只允许清理普通文件")
	}
	script := `function run(argv) {
ObjC.import("Foundation");
var url = $.NSURL.fileURLWithPath(argv[0]);
var result = Ref();
var error = Ref();
var ok = $.NSFileManager.defaultManager.trashItemAtURLResultingItemURLError(url, result, error);
if (!ok) throw new Error(ObjC.unwrap(error[0].localizedDescription));
}`
	output, runErr := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", script, path).CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	return nil
}
