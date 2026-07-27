# 空间卫士 Space Sheriff

一个完全本地运行的 Windows/macOS 磁盘大文件分析工具。项目提供两种界面：

- **便携版（推荐）**：单文件、Windows/macOS 共用同一套 Go 核心，启动后自动打开本地网页界面。
- **macOS 原生窗口版**：Apple Silicon `.app`，使用 Tk 9 原生窗口。

## 功能

- 选择磁盘或文件夹，按体积找出大文件
- 显示磁盘总容量、已用空间和可用空间（便携版）
- 显示文件路径、大小和最后修改时间
- 使用保守、可解释的规则判断“通常可清理 / 需人工确认 / 不建议删除”
- 系统目录中的文件会被保护
- 清理操作只移入系统回收站，不做永久删除
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

Go 便携版只使用标准库，可通过 `GOOS` 和 `GOARCH` 交叉编译。

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

## 安全设计

- 不跟随符号链接，避免重复扫描和越界
- 系统目录结果禁止调用清理接口
- 清理接口只接受当前扫描结果中的普通文件
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
```

## 参与贡献与许可证

贡献前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。安全问题请按
[SECURITY.md](SECURITY.md) 私密报告。

本项目采用 [MIT License](LICENSE)。
