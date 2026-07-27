package main

import (
	"path/filepath"
	"strings"
	"time"
)

type Advice struct {
	Level  string `json:"level"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
	Score  int    `json:"score"`
}

var coreSystemParts = words("system32 syswow64 bin sbin usr etc")
var protectedParts = set("windows", "system32", "syswow64", "program files", "program files (x86)", "programdata", "system", "library", "applications", "bin", "sbin", "usr", "etc", "var")
var personalParts = words("desktop documents pictures photos movies music downloads 桌面 文档 图片 影片 音乐 下载")
var cacheParts = set("cache", "caches", "tmp", "temp", "temporary files", "crashdumps", "logs")
var installerExts = words(".dmg .pkg .iso .msi .exe")
var archiveExts = words(".zip .7z .rar .tar .gz .bz2")
var personalExts = words(".doc .docx .xls .xlsx .ppt .pptx .pdf .pages .numbers .key .jpg .jpeg .png .heic .mov .mp4")

func words(value string) map[string]bool {
	result := make(map[string]bool)
	for _, item := range strings.Fields(value) {
		result[item] = true
	}
	return result
}

func set(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func pathParts(path string) map[string]bool {
	result := make(map[string]bool)
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		result[strings.ToLower(part)] = true
	}
	return result
}

func intersects(parts, candidates map[string]bool) bool {
	for part := range parts {
		if candidates[part] {
			return true
		}
	}
	return false
}

func advise(path string, size int64, modified, now time.Time) Advice {
	parts := pathParts(path)
	ext := strings.ToLower(filepath.Ext(path))
	age := int(now.Sub(modified).Hours() / 24)
	if age < 0 {
		age = 0
	}

	if intersects(parts, coreSystemParts) {
		return Advice{"danger", "不建议删除", "位于核心系统目录，删除可能导致系统或软件无法运行。", 0}
	}
	if intersects(parts, personalParts) || personalExts[ext] {
		return Advice{"review", "需人工确认", "可能是个人文件。请先预览或备份，再决定是否删除。", 35}
	}
	if intersects(parts, cacheParts) {
		if age >= 7 {
			score := 75
			if age >= 30 {
				score = 90
			}
			return Advice{"safe", "通常可清理", "位于缓存、临时或日志目录，已较长时间未修改，通常可由应用重新生成。", score}
		}
		return Advice{"review", "需人工确认", "位于缓存、临时或日志目录，但文件较新，可能仍在使用。", 45}
	}
	if intersects(parts, protectedParts) {
		return Advice{"danger", "不建议删除", "位于系统或应用目录，删除可能导致系统或软件无法运行。", 0}
	}
	if installerExts[ext] && age >= 30 {
		return Advice{"safe", "通常可清理", "这是较旧的安装镜像或安装包；确认软件已安装后可移入回收站。", 80}
	}
	if archiveExts[ext] && age >= 90 {
		return Advice{"review", "可考虑清理", "这是较旧的压缩包；请确认内容已有副本或已解压。", 60}
	}
	if size >= 10*1024*1024*1024 && age >= 180 {
		return Advice{"review", "可考虑清理", "文件超过 10 GB 且长期未修改，但用途未知。", 55}
	}
	return Advice{"review", "需人工确认", "未发现明确的安全清理特征，请确认用途后再处理。", 40}
}
