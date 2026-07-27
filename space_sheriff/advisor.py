from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import time


@dataclass(frozen=True)
class Advice:
    level: str
    label: str
    reason: str
    score: int


_PROTECTED_PARTS = {
    "windows",
    "system32",
    "syswow64",
    "program files",
    "program files (x86)",
    "programdata",
    "system",
    "library",
    "applications",
    "bin",
    "sbin",
    "usr",
    "etc",
    "var",
}
_CORE_SYSTEM_PARTS = {
    "system32",
    "syswow64",
    "bin",
    "sbin",
    "usr",
    "etc",
}
_PERSONAL_PARTS = {
    "desktop",
    "documents",
    "pictures",
    "photos",
    "movies",
    "music",
    "downloads",
    "桌面",
    "文档",
    "图片",
    "影片",
    "音乐",
    "下载",
}
_CACHE_PARTS = {
    "cache",
    "caches",
    "tmp",
    "temp",
    "temporary files",
    "crashdumps",
    "logs",
}
_INSTALLER_SUFFIXES = {".dmg", ".pkg", ".iso", ".msi", ".exe"}
_ARCHIVE_SUFFIXES = {".zip", ".7z", ".rar", ".tar", ".gz", ".bz2"}
_PERSONAL_SUFFIXES = {
    ".doc",
    ".docx",
    ".xls",
    ".xlsx",
    ".ppt",
    ".pptx",
    ".pdf",
    ".pages",
    ".numbers",
    ".key",
    ".jpg",
    ".jpeg",
    ".png",
    ".heic",
    ".mov",
    ".mp4",
}


def advise(path: Path, size: int, modified_at: float, now: float | None = None) -> Advice:
    """Return conservative, explainable deletion advice for a file."""
    now = time.time() if now is None else now
    parts = {part.casefold() for part in path.parts}
    suffix = path.suffix.casefold()
    age_days = max(0, int((now - modified_at) / 86400))

    if parts & _CORE_SYSTEM_PARTS:
        return Advice(
            "danger",
            "不建议删除",
            "位于系统或应用目录，删除可能导致系统或软件无法运行。",
            0,
        )
    if parts & _PERSONAL_PARTS or suffix in _PERSONAL_SUFFIXES:
        return Advice(
            "review",
            "需人工确认",
            "可能是个人文件。请先预览或备份，再决定是否删除。",
            35,
        )
    if parts & _CACHE_PARTS:
        return Advice(
            "safe" if age_days >= 7 else "review",
            "通常可清理" if age_days >= 7 else "需人工确认",
            f"位于缓存、临时或日志目录，已 {age_days} 天未修改。"
            + ("通常可由应用重新生成。" if age_days >= 7 else "文件较新，可能仍在使用。"),
            90 if age_days >= 30 else 75 if age_days >= 7 else 45,
        )
    if parts & _PROTECTED_PARTS:
        return Advice(
            "danger",
            "不建议删除",
            "位于系统或应用目录，删除可能导致系统或软件无法运行。",
            0,
        )
    if suffix in _INSTALLER_SUFFIXES and age_days >= 30:
        return Advice(
            "safe",
            "通常可清理",
            f"这是安装镜像或安装包，已 {age_days} 天未修改；确认软件已安装后可移入回收站。",
            80,
        )
    if suffix in _ARCHIVE_SUFFIXES and age_days >= 90:
        return Advice(
            "review",
            "可考虑清理",
            f"这是压缩包，已 {age_days} 天未修改；确认内容已有副本或已解压。",
            60,
        )
    if size >= 10 * 1024**3 and age_days >= 180:
        return Advice(
            "review",
            "可考虑清理",
            f"文件超过 10 GB 且已 {age_days} 天未修改，但用途未知。",
            55,
        )
    return Advice(
        "review",
        "需人工确认",
        f"未发现明确的安全清理特征，已 {age_days} 天未修改。",
        40,
    )
