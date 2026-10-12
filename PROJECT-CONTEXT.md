# Space Sheriff 项目上下文（冻结快照）

更新时间：2026-10-06

## 当前结论

Space Sheriff 已达到可本地使用的 v1.0 稳定候选状态。核心扫描、重复文件识别、清理计划、执行前安全检查、系统废纸篓、历史变化对比和 macOS 通用应用打包均已完成。

建议从现在起冻结功能开发。除非准备公开分发，否则无需继续增加功能、重构或引入框架。

## 项目是什么

Space Sheriff 是一个本地磁盘空间分析与安全清理工具：

- 扫描 Windows/macOS 目录中的大文件
- 通过 SHA-256 识别重复文件
- 生成清理计划，执行时移入系统废纸篓
- 所有数据和界面都在本机运行
- 使用 Go 可执行程序、本地 Web UI 和 SQLite 存储
- 支持扫描计划、容量预算和基础治理能力

项目不依赖云服务，也不要求用户预装 Go、Node.js、Python、SQLite 或 Docker。

## 本轮完成的三项增强

### 1. 清理前时光机

在真正执行前预览清理结果：预计释放空间、清理后可用空间、磁盘使用率、分类构成和建议，并支持导出 JSON。

关键文件：

- `portable/web/cleanup-preview.js`
- `portable/web/cleanup-preview.test.cjs`
- `portable/web/app.js`
- `portable/web/index.html`
- `portable/web/style.css`

视觉证据：`cleanup-preview-proof.jpg`

### 2. 起飞前检查

新增 `/api/plan/preflight`，并在前后端双重验证：

- 文件仍属于当前扫描结果
- 目标是普通文件而非符号链接
- 文件大小和修改时间没有变化
- 路径不属于受保护区域
- 重复文件组至少保留一份

只有检查通过时才能执行；点击执行时会再次检查，后端执行入口也不会绕过验证。

关键文件：

- `portable/main.go`
- `portable/main_test.go`

视觉证据：`preflight-safety-proof.jpg`

### 3. 空间变化雷达

比较同一路径最近两次完成的扫描，显示：

- 新进入结果的文件
- 变大的文件
- 变小的文件
- 离开结果的文件
- 净空间变化

“离开结果”只表示文件不再出现在当前扫描结果中，不宣称文件已被删除。

此功能复用了现有数据库结构，没有新增迁移。

关键文件：

- `portable/scanner.go`
- `portable/store.go`
- `portable/store_test.go`
- `portable/web/app.js`
- `portable/web/index.html`
- `portable/web/style.css`

视觉证据：`space-change-radar-proof.jpg`

## 最新交付物

推荐使用：

- `dist/SpaceSheriff-v1.0.0-macOS-Universal-radar-v3.zip`
- `dist/SpaceSheriff-v1.0.0-macOS-Universal-radar-v3.zip.sha256`

SHA-256：

```text
b495d3d03dffb28480b758810d8830ee53908d8626c3a27cebd6aba596bf9d3c
```

该应用包含 arm64 和 x86_64 两种 macOS 架构，已做 ad-hoc 签名。解压后可直接使用，不需要提前安装编程语言、框架或数据库。

由于没有 Apple Developer ID 签名和公证，首次启动可能需要在 Finder 中右键应用并选择“打开”。

旧版压缩包保留在 `dist/` 中，仅用于回退。

## 已完成验证

以下检查在最新改动后均已通过：

```text
go test ./...
go vet ./...
node --check web/app.js
node --test web/cleanup-preview.test.cjs
git diff --check
```

打包产物还验证了：

- SHA-256 校验一致
- codesign 结构有效
- 可执行文件同时包含 x86_64 和 arm64
- `--version` 输出 1.0.0
- 变化雷达端到端场景覆盖新增、变大、变小、离开结果四类变化
- 浏览器验证期间控制台错误为 0

## 已知边界

这些不是当前本地版本的阻塞项：

- 未做 Apple Developer ID 正式签名与公证
- 未完成 Windows 真机安装、计划任务和签名验证
- 未完成 macOS LaunchAgent 权限生命周期的外部真机验证
- 未做十万到百万文件、磁盘写满、外置盘拔出、断电或强杀进程压力测试
- 未做 VoiceOver、Narrator、高对比度和完整键盘流程的人工验收

只有在准备大范围公开分发、跨平台交付或企业使用时，才值得继续处理这些事项。

## 当前工作区状态

本轮代码和截图仍是未提交改动。主要涉及：

- `.github/workflows/ci.yml`
- `README.md`
- `portable/` 下的后端、测试和 Web UI
- 三张功能验证截图
- 本上下文文档

因此项目在“功能完成、验证通过、尚未形成最终 Git 提交”的状态。不要使用会丢失本地修改的 Git 重置命令。

## 推荐收尾方式

必须决定的只有一件事：是否把当前状态做成最终 Git 提交或标签。

如果只是自己使用，可直接保存最新 ZIP、校验文件和当前项目目录，然后停止开发。

如果希望形成正式里程碑，下一步只做一次干净的最终提交，并可标记 `v1.0.0-local`；不要顺手继续改功能。

## 将来恢复项目时

1. 先阅读本文和 `README.md`。
2. 查看 `git status`，确认本地改动没有丢失。
3. 运行上述五项验证。
4. 从最新 ZIP 开始人工验收，而不是重新设计架构。
5. 只有出现真实需求或可复现问题时才恢复开发。

