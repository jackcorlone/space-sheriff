# Changelog

本项目的显著变更记录在此文件中。版本号遵循
[Semantic Versioning](https://semver.org/)。

## Unreleased

### Added

- 每日或每周定时只读扫描计划
- macOS LaunchAgent 与 Windows Task Scheduler 当前用户任务注册
- 不启动浏览器或 HTTP 服务的 `--scheduled-scan` 单次运行模式
- 计划扫描历史、策略快照和有限的大文件结果详情
- SQLite 跨进程计划租约与过期恢复

### Changed

- SQLite schema 升级到 v3，扫描会话记录触发来源和策略版本
- 中断恢复只处理超过租约窗口的陈旧任务，避免误改其他活动进程

### Security

- 定时入口不调用清理、回收站或清理计划写入
- 系统计划任务只继承当前用户权限，不注册系统级服务

## 0.2.0 - 2026-07-28

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
