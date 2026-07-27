from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from collections.abc import Callable
import os
import time

from .advisor import Advice, advise


@dataclass(frozen=True)
class FileRecord:
    path: Path
    size: int
    modified_at: float
    advice: Advice


@dataclass(frozen=True)
class ScanStats:
    files_seen: int
    bytes_seen: int
    errors: int
    elapsed: float


Progress = Callable[[int, int, str], None]


def scan_largest(
    root: Path,
    minimum_size: int,
    limit: int,
    cancelled: Callable[[], bool] = lambda: False,
    progress: Progress | None = None,
) -> tuple[list[FileRecord], ScanStats]:
    """Scan without following symlinks and return the largest matching files."""
    started = time.monotonic()
    records: list[FileRecord] = []
    files_seen = bytes_seen = errors = 0

    def on_error(_: OSError) -> None:
        nonlocal errors
        errors += 1

    for directory, dirnames, filenames in os.walk(root, topdown=True, onerror=on_error):
        if cancelled():
            break
        dirnames[:] = [
            name
            for name in dirnames
            if not Path(directory, name).is_symlink()
        ]
        for name in filenames:
            if cancelled():
                break
            path = Path(directory, name)
            try:
                stat = os.stat(path, follow_symlinks=False)
                if not path.is_file() or path.is_symlink():
                    continue
            except (OSError, PermissionError):
                errors += 1
                continue
            files_seen += 1
            bytes_seen += stat.st_size
            if stat.st_size >= minimum_size:
                records.append(
                    FileRecord(path, stat.st_size, stat.st_mtime, advise(path, stat.st_size, stat.st_mtime))
                )
            if progress and files_seen % 250 == 0:
                progress(files_seen, bytes_seen, directory)

    records.sort(key=lambda item: item.size, reverse=True)
    return records[:limit], ScanStats(
        files_seen,
        bytes_seen,
        errors,
        time.monotonic() - started,
    )
