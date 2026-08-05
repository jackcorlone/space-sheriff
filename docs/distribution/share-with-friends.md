# 面向朋友分享 Space Sheriff

本文把分享分成“源码协作”和“普通用户直接运行”两种方式。当前 GitHub 主仓库是
`https://github.com/jackcorlone/space-sheriff`，v1.0 稳定候选位于 `agent/v0.6` 分支；
Draft PR 合并后再把 `main` 作为普通用户的默认版本。

## 1. 推荐的双轨方式

- GitHub 保留主仓库、提交历史和代码审查；
- Gitee 建立面向中国朋友的镜像或副本；
- Gitee Release 或网盘提供无需安装 Go 的 Windows/macOS 压缩包；
- 不把本地 SQLite 数据库、浏览器令牌或个人扫描路径放进压缩包。

Gitee 的“导入已有仓库”可以直接从 GitHub 建立仓库；也可以在本地同时添加两个远程地址，
由你决定何时同步。创建 Gitee 仓库时建议保持为空，不要先生成 README，以免首次推送产生无关冲突。

## 2. 创建 Gitee 镜像

在 Gitee 创建空仓库后，在本地执行：

```bash
git remote add gitee https://gitee.com/你的账号/space-sheriff.git
git push -u gitee agent/v0.6
```

如果已经合并 v1.0 PR，改为推送主分支：

```bash
git push -u gitee main
```

朋友获取当前稳定候选：

```bash
git clone -b agent/v0.6 --single-branch \
  https://gitee.com/你的账号/space-sheriff.git
```

不要把 `agent/v0.6` 误写成 `main`；当前两者内容可能不同。

## 3. 构建可直接运行包

在 macOS 或 Linux 开发机执行：

```bash
./scripts/package_portable.sh
```

脚本会生成 `dist/portable-v1.0.0/`，包括：

- `SpaceSheriff-v1.0.0-macos-amd64.zip`
- `SpaceSheriff-v1.0.0-macos-arm64.zip`
- `SpaceSheriff-v1.0.0-windows-amd64.zip`
- `SpaceSheriff-v1.0.0-windows-arm64.zip`
- `SHA256SUMS.txt`

每个压缩包都带有 `README-FIRST.txt`、许可证和 v1.0 发布说明。它们是未签名内部测试包，
不能替代正式安装器；正式发布前仍需完成代码签名、公证、Task Scheduler/LaunchAgent、百万文件和断电恢复验收。

## 4. 给普通朋友的最短说明

```text
下载与你电脑匹配的 SpaceSheriff 压缩包，解压后运行程序即可。
它只在本机分析磁盘，不上传文件；扫描和定时任务不会自动删除文件。
如果系统显示“未签名”提醒，请先确认压缩包来源，不要关闭系统安全功能。
```

开发者朋友可以克隆源码；普通用户不应被要求安装 Go 或直接打开 `portable/web/index.html`。

## 5. 分享前检查

```bash
git ls-files | rg '(^|/)(\\.local-data|\\.env|.*\\.(key|pem|p12|pfx|cer))($|/)'
```

命令没有输出才可以继续打包。不要分享带 `?token=...` 的本地页面地址，也不要打包
`portable/.local-data/`、构建缓存或个人下载目录。
