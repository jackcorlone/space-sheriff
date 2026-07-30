# 空间卫士 Space Sheriff

一个完全本地运行的 Windows/macOS 磁盘大文件分析工具。项目提供两种界面：

- **便携版（推荐）**：单文件、Windows/macOS 共用同一套 Go 核心，启动后自动打开本地网页界面。
- **macOS 原生窗口版**：Apple Silicon `.app`，使用 Tk 9 原生窗口。

## 功能

- 选择磁盘或文件夹，按体积找出大文件
- 按目录聚合占用空间，并逐层定位体积最大的文件夹（便携版 v0.2）
- 显示磁盘总容量、已用空间和可用空间（便携版）
- 显示文件路径、大小和最后修改时间
- 按名称、风险、类型筛选与排序，并可批量选择文件（便携版 v0.2）
- 使用完整 SHA-256 内容指纹寻找重复文件，不仅比较名称或大小（便携版 v0.3）
- 通过清理计划集中复核文件，并保证每组重复文件至少保留一个副本（便携版 v0.3）
- 设置扫描排除规则，并将结果导出为 JSON 或 CSV（便携版 v0.3）
- 使用本地 SQLite 保存扫描索引与清理计划，应用重启后仍可恢复（便携版 v0.4）
- 未变化文件可复用 SHA-256；硬链接不会被误报为重复副本（便携版 v0.4）
- 清理前记录事务意图、逐项结果和实际释放空间，异常中断后保留审计记录（便携版 v0.4）
- 内置稳健、均衡、空间优先策略，并支持严格校验的本地 JSON 策略（便携版 v0.5）
- 治理中心展示清理事务、逐项结果、数据库完整性和索引规模（便携版 v0.5）
- 支持安全的 WAL checkpoint 与 SQLite optimize，不删除索引或审计记录（便携版 v0.5）
- 创建每日或每周只读扫描计划，由 macOS LaunchAgent 或 Windows Task Scheduler 触发（便携版 v0.6）
- 无界面定时扫描保存运行统计和有限的大文件结果，支持立即运行、启停与历史查看（便携版 v0.6）
- SQLite 租约阻止同一计划并发执行，系统任务注册失败会在界面中明确展示（便携版 v0.6）
- 使用保守、可解释的规则判断“通常可清理 / 需人工确认 / 不建议删除”
- 系统目录中的文件会被保护
- 清理操作只移入系统回收站，不做永久删除；执行前会复核文件是否在扫描后发生变化
- 扫描不上传任何文件名或数据

> “通常可清理”不是绝对安全保证。应用会展示判断依据，并在每次清理前要求确认。

## 直接使用

前往仓库的 **Releases** 页面下载与你的系统对应的文件。

### Windows

- 普通 Intel/AMD 电脑：双击 `SpaceSheriff-Windows-x64.exe`
- Windows ARM 电脑：双击 `SpaceSheriff-Windows-ARM64.exe`

程序不会显示命令行窗口，而是自动打开浏览器中的本地界面。地址只监听
`127.0.0.1`，没有互联网服务。

### macOS

- Apple Silicon（M1/M2/M3/M4/M5）：解压 `SpaceSheriff-macOS-Apple-Silicon-App.zip`，
  在 Finder 中右键 `SpaceSheriff.app` 并选择“打开”
- Intel Mac：运行便携版 `SpaceSheriff-macOS-Intel`

未配置 Apple Developer ID，因此首次启动可能显示未签名应用提示。

## 本地运行

```bash
python3 app.py
```

需要带新版 Tk 支持的 Python；新版 macOS 建议安装 Homebrew 的
`python@3.14` 与 `python-tk@3.14`。

便携版开发运行：

```bash
cd portable
go run .
```

## 构建

推荐便携版：

```bash
cd portable
go test ./...
go build .
```

Go 便携版使用纯 Go 依赖，无需 CGO，可通过 `GOOS` 和 `GOARCH` 交叉编译。

原生窗口版：

安装构建依赖：

```bash
python -m pip install -r requirements-build.txt
pyinstaller --noconfirm SpaceSheriff.spec
```

- Windows 产物：`dist/SpaceSheriff.exe`
- macOS 产物：`dist/SpaceSheriff.app`

PyInstaller 不能跨系统打包。仓库内的 GitHub Actions 会分别在 Windows 与 macOS
环境测试项目；推送 `v*` 标签后，Release 工作流会构建四个平台便携版、macOS
原生应用并创建 GitHub Release。
也可以在 Windows 双击 `build_windows.bat`，或在 macOS 终端运行
`sh build_macos.command`。

macOS 未签名应用首次打开时，需在 Finder 中右键应用并选择“打开”。正式分发建议配置
Apple Developer ID 签名与公证；Windows 正式分发建议配置代码签名证书，以减少系统警告。

## 智能判断边界

当前版本采用离线规则，不调用云端 AI：

- 系统、应用目录：不建议删除
- 个人文档、照片、视频：始终要求人工确认
- 较旧的缓存、临时文件和日志：通常可清理
- 较旧的安装包：确认已安装后通常可清理
- 压缩包和用途未知的大文件：只提示考虑清理

这种方案保护隐私、结果可解释，也不会因网络或 API 密钥而失效。

策略切换只影响后续扫描。已有扫描结果和清理计划不会被静默重算；需要按新策略重新扫描。
v0.6 的定时任务即使在界面退出后也可由操作系统启动，但只做扫描、索引和本地报告，绝不自动清理。
本版本不包含云端策略分发或自动更新服务。

## 安全设计

- 不跟随符号链接，避免重复扫描和越界
- 系统目录结果禁止调用清理接口
- 清理接口只接受当前扫描结果中的普通文件
- 每次启动生成独立会话令牌，限制本地 API 只能由本次界面调用
- 批量清理逐文件校验，单个失败不会掩盖其他文件的处理结果
- 重复文件清理计划不能一次删除同组的全部副本
- 清理计划、操作意图和逐项结果保存在本机 SQLite 数据库；不保存文件内容
- 通过设备/inode（macOS）或卷/文件索引（Windows）识别硬链接
- 清理中断会保留审计记录，但系统回收站操作与数据库无法组成自动回滚事务
- 自定义策略只能调整有范围限制的时间与体积阈值，不能关闭系统或个人文件保护
- 清理事务固化当时的策略 ID 和版本，便于解释历史判断来源
- 定时扫描使用当前用户权限，不安装常驻服务或 SYSTEM 任务
- 后台入口不调用回收站或清理计划；跨进程租约阻止同一计划重叠运行
- 计划配置先保存为期望状态，系统注册错误不会被静默忽略
- Windows 使用系统 `SHFileOperationW` 回收站接口
- macOS 使用系统 `NSFileManager.trashItemAtURL` 接口
- 本地服务校验 Host 与 JSON Content-Type，降低其他网页调用本地接口的风险

## 项目结构

```text
portable/              Go 便携版与嵌入式本地界面
space_sheriff/         Python 原生窗口版核心
tests/                 Python 测试
.github/workflows/     CI 与自动发布
docs/releases/         版本发布说明
docs/architecture/     架构与可靠性设计
```

## 参与贡献与许可证

贡献前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。安全问题请按
[SECURITY.md](SECURITY.md) 私密报告。

本项目采用 [MIT License](LICENSE)。
