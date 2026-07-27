# Changelog

本项目的显著变更记录在此文件中。版本号遵循
[Semantic Versioning](https://semver.org/)。

## Unreleased

### Added

- 目录空间聚合与逐层下钻，快速定位占用最大的文件夹
- 文件名称搜索、风险与类型筛选、大小与时间排序
- 最多 500 个扫描结果的批量回收站清理
- 更细分且带稳定规则编号的离线清理建议

### Security

- 每次启动生成本地 API 会话令牌
- 清理前重新核对文件类型、大小和修改时间，阻止处理扫描后已变化的文件

## 0.1.0 - 2026-07-27

### Added

- Windows x64、Windows ARM64、macOS Apple Silicon 和 macOS Intel 便携版
- 本地磁盘容量与可用空间检测
- 大文件扫描、大小排序、路径和最后修改时间展示
- 离线、可解释的删除风险判断
- 系统与应用目录保护
- Windows 和 macOS 系统回收站集成
- macOS Apple Silicon 原生窗口版
- 自动测试、跨平台构建与 GitHub Release 工作流
