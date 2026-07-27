from __future__ import annotations

from pathlib import Path
import os
import queue
import shutil
import subprocess
import sys
import threading
import tkinter as tk
from tkinter import filedialog, messagebox, ttk

from space_sheriff.formatting import human_size, local_time
from space_sheriff.scanner import FileRecord, scan_largest
from space_sheriff.trash import move_to_trash


class SpaceSheriffApp(tk.Tk):
    def __init__(self) -> None:
        super().__init__()
        self.title("空间卫士 Space Sheriff")
        self.geometry("1120x720")
        self.minsize(900, 560)
        self.records: list[FileRecord] = []
        self.events: queue.Queue[tuple] = queue.Queue()
        self.cancel_event = threading.Event()
        self.worker: threading.Thread | None = None
        self._configure_style()
        self._build()
        self.after(100, self._process_events)

    def _configure_style(self) -> None:
        style = ttk.Style(self)
        for theme in ("aqua", "vista", "clam"):
            if theme in style.theme_names():
                style.theme_use(theme)
                break
        style.configure("Treeview", rowheight=28)
        style.configure("Title.TLabel", font=("", 20, "bold"))
        style.configure("Hint.TLabel", foreground="#667085")

    def _build(self) -> None:
        frame = ttk.Frame(self, padding=18)
        frame.pack(fill="both", expand=True)

        ttk.Label(frame, text="空间卫士", style="Title.TLabel").pack(anchor="w")
        ttk.Label(
            frame,
            text="本地扫描大文件，给出可解释的清理建议。所有分析均在本机完成。",
            style="Hint.TLabel",
        ).pack(anchor="w", pady=(2, 14))

        controls = ttk.Frame(frame)
        controls.pack(fill="x")
        ttk.Label(controls, text="扫描位置").grid(row=0, column=0, sticky="w")
        self.path_var = tk.StringVar(value=str(Path.home()))
        ttk.Entry(controls, textvariable=self.path_var).grid(
            row=1, column=0, sticky="ew", padx=(0, 8)
        )
        ttk.Button(controls, text="选择…", command=self._choose).grid(row=1, column=1)
        ttk.Label(controls, text="最小文件").grid(row=0, column=2, padx=(16, 0))
        self.minimum_var = tk.StringVar(value="500 MB")
        ttk.Combobox(
            controls,
            textvariable=self.minimum_var,
            values=("100 MB", "500 MB", "1 GB", "5 GB"),
            width=9,
            state="readonly",
        ).grid(row=1, column=2, padx=(16, 8))
        self.scan_button = ttk.Button(controls, text="开始扫描", command=self._start_scan)
        self.scan_button.grid(row=1, column=3, padx=(0, 8))
        self.cancel_button = ttk.Button(
            controls, text="停止", command=self.cancel_event.set, state="disabled"
        )
        self.cancel_button.grid(row=1, column=4)
        controls.columnconfigure(0, weight=1)

        self.status_var = tk.StringVar(value="准备就绪")
        ttk.Label(frame, textvariable=self.status_var, style="Hint.TLabel").pack(
            fill="x", pady=(12, 6)
        )
        self.progress = ttk.Progressbar(frame, mode="indeterminate")
        self.progress.pack(fill="x", pady=(0, 10))

        columns = ("size", "modified", "advice", "path")
        self.tree = ttk.Treeview(frame, columns=columns, show="headings", selectmode="browse")
        self.tree.heading("size", text="大小")
        self.tree.heading("modified", text="修改时间")
        self.tree.heading("advice", text="删除建议")
        self.tree.heading("path", text="路径")
        self.tree.column("size", width=95, anchor="e", stretch=False)
        self.tree.column("modified", width=145, stretch=False)
        self.tree.column("advice", width=120, stretch=False)
        self.tree.column("path", width=650)
        scrollbar = ttk.Scrollbar(frame, orient="vertical", command=self.tree.yview)
        self.tree.configure(yscrollcommand=scrollbar.set)
        self.tree.pack(side="left", fill="both", expand=True)
        scrollbar.pack(side="left", fill="y")
        self.tree.bind("<<TreeviewSelect>>", self._selection_changed)
        self.tree.bind("<Double-1>", lambda _: self._reveal())

        side = ttk.Frame(frame, width=260, padding=(14, 0, 0, 0))
        side.pack(side="right", fill="y", before=self.tree)
        side.pack_propagate(False)
        ttk.Label(side, text="判断说明", font=("", 13, "bold")).pack(anchor="w")
        self.reason_var = tk.StringVar(value="选择一个文件查看判断依据。")
        ttk.Label(
            side, textvariable=self.reason_var, wraplength=240, justify="left"
        ).pack(anchor="w", fill="x", pady=(8, 20))
        self.reveal_button = ttk.Button(side, text="在文件管理器中显示", command=self._reveal)
        self.reveal_button.pack(fill="x", pady=(0, 8))
        self.trash_button = ttk.Button(side, text="移入回收站", command=self._trash)
        self.trash_button.pack(fill="x")

    def _choose(self) -> None:
        selected = filedialog.askdirectory(initialdir=self.path_var.get())
        if selected:
            self.path_var.set(selected)

    def _start_scan(self) -> None:
        root = Path(self.path_var.get()).expanduser()
        if not root.is_dir():
            messagebox.showerror("无法扫描", "请选择一个存在的文件夹或磁盘。")
            return
        minimum = {
            "100 MB": 100 * 1024**2,
            "500 MB": 500 * 1024**2,
            "1 GB": 1024**3,
            "5 GB": 5 * 1024**3,
        }[self.minimum_var.get()]
        self.cancel_event.clear()
        self.records.clear()
        self.tree.delete(*self.tree.get_children())
        self.scan_button.configure(state="disabled")
        self.cancel_button.configure(state="normal")
        self.progress.start(12)
        self.status_var.set("正在扫描…")

        def run() -> None:
            try:
                records, stats = scan_largest(
                    root,
                    minimum,
                    2000,
                    self.cancel_event.is_set,
                    lambda count, total, current: self.events.put(
                        ("progress", count, total, current)
                    ),
                )
                self.events.put(("done", records, stats, self.cancel_event.is_set()))
            except Exception as exc:
                self.events.put(("error", str(exc)))

        self.worker = threading.Thread(target=run, daemon=True)
        self.worker.start()

    def _process_events(self) -> None:
        try:
            while True:
                event = self.events.get_nowait()
                if event[0] == "progress":
                    _, count, total, current = event
                    self.status_var.set(
                        f"已检查 {count:,} 个文件（{human_size(total)}） · {current}"
                    )
                elif event[0] == "done":
                    self._scan_done(*event[1:])
                elif event[0] == "error":
                    self._scan_error(event[1])
        except queue.Empty:
            pass
        self.after(100, self._process_events)

    def _scan_done(self, records, stats, cancelled: bool) -> None:
        self.records = records
        for index, item in enumerate(records):
            self.tree.insert(
                "",
                "end",
                iid=str(index),
                values=(
                    human_size(item.size),
                    local_time(item.modified_at),
                    item.advice.label,
                    str(item.path),
                ),
            )
        self.progress.stop()
        self.scan_button.configure(state="normal")
        self.cancel_button.configure(state="disabled")
        prefix = "扫描已停止" if cancelled else "扫描完成"
        self.status_var.set(
            f"{prefix} · 检查 {stats.files_seen:,} 个文件 · "
            f"找到 {len(records):,} 个大文件 · {stats.errors:,} 个项目无权限读取 · "
            f"{stats.elapsed:.1f} 秒"
        )

    def _scan_error(self, detail: str) -> None:
        self.progress.stop()
        self.scan_button.configure(state="normal")
        self.cancel_button.configure(state="disabled")
        self.status_var.set("扫描失败")
        messagebox.showerror("扫描失败", detail)

    def _selected(self) -> FileRecord | None:
        selection = self.tree.selection()
        return self.records[int(selection[0])] if selection else None

    def _selection_changed(self, _: object = None) -> None:
        record = self._selected()
        self.reason_var.set(record.advice.reason if record else "选择一个文件查看判断依据。")

    def _reveal(self) -> None:
        record = self._selected()
        if not record:
            return
        try:
            if sys.platform == "darwin":
                subprocess.run(["open", "-R", str(record.path)], check=True)
            elif os.name == "nt":
                subprocess.run(["explorer", "/select,", str(record.path)], check=True)
            else:
                raise RuntimeError("仅支持 Windows 和 macOS。")
        except Exception as exc:
            messagebox.showerror("无法显示文件", str(exc))

    def _trash(self) -> None:
        record = self._selected()
        if not record:
            messagebox.showinfo("请选择文件", "请先选择一个文件。")
            return
        if record.advice.level == "danger":
            messagebox.showwarning("已阻止", "此文件位于受保护目录，空间卫士不会清理它。")
            return
        if not messagebox.askyesno(
            "确认移入回收站",
            f"{record.path}\n\n大小：{human_size(record.size)}\n"
            f"建议：{record.advice.label}\n依据：{record.advice.reason}\n\n"
            "文件将移入系统回收站，可在回收站中恢复。是否继续？",
        ):
            return
        try:
            move_to_trash(record.path)
        except Exception as exc:
            messagebox.showerror("清理失败", str(exc))
            return
        index = self.records.index(record)
        self.records[index] = FileRecord(
            record.path, 0, record.modified_at, record.advice
        )
        self.tree.delete(str(index))
        self.reason_var.set(f"已移入回收站，释放 {human_size(record.size)}。")


if __name__ == "__main__":
    SpaceSheriffApp().mainloop()
