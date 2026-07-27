from __future__ import annotations

from pathlib import Path
import ctypes
import os
import platform
import subprocess


def move_to_trash(path: Path) -> None:
    """Move one file to the platform trash. Never permanently deletes."""
    path = path.resolve(strict=True)
    if not path.is_file():
        raise ValueError("当前版本只允许清理普通文件。")

    system = platform.system()
    if system == "Windows":
        _windows_trash(path)
    elif system == "Darwin":
        _macos_trash(path)
    else:
        raise RuntimeError("当前仅支持 Windows 和 macOS 的回收站。")


def _windows_trash(path: Path) -> None:
    from ctypes import wintypes

    class SHFILEOPSTRUCTW(ctypes.Structure):
        _fields_ = [
            ("hwnd", wintypes.HWND),
            ("wFunc", wintypes.UINT),
            ("pFrom", wintypes.LPCWSTR),
            ("pTo", wintypes.LPCWSTR),
            ("fFlags", ctypes.c_ushort),
            ("fAnyOperationsAborted", wintypes.BOOL),
            ("hNameMappings", ctypes.c_void_p),
            ("lpszProgressTitle", wintypes.LPCWSTR),
        ]

    operation = SHFILEOPSTRUCTW()
    operation.wFunc = 3  # FO_DELETE
    operation.pFrom = str(path) + "\0\0"
    operation.fFlags = 0x0040 | 0x0010 | 0x0400  # undo, no confirm, no UI
    result = ctypes.windll.shell32.SHFileOperationW(ctypes.byref(operation))
    if result != 0 or operation.fAnyOperationsAborted:
        raise OSError(f"Windows 回收站操作失败（代码 {result}）。")


def _macos_trash(path: Path) -> None:
    script = """
function run(argv) {
    ObjC.import("Foundation");
    var url = $.NSURL.fileURLWithPath(argv[0]);
    var result = Ref();
    var error = Ref();
    var ok = $.NSFileManager.defaultManager.trashItemAtURLResultingItemURLError(
        url, result, error
    );
    if (!ok) throw new Error(ObjC.unwrap(error[0].localizedDescription));
}
"""
    completed = subprocess.run(
        ["/usr/bin/osascript", "-l", "JavaScript", "-e", script, str(path)],
        capture_output=True,
        text=True,
        check=False,
    )
    if completed.returncode:
        raise OSError(completed.stderr.strip() or "移入废纸篓失败。")
