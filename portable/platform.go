package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type RootInfo struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
}

func availableRoots() []RootInfo {
	var paths []string
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		for letter := 'A'; letter <= 'Z'; letter++ {
			path := fmt.Sprintf("%c:\\", letter)
			if _, err := os.Stat(path); err == nil {
				paths = append(paths, path)
			}
		}
	} else {
		paths = append(paths, "/")
		if home != "" {
			paths = append(paths, home)
		}
		volumes, _ := filepath.Glob("/Volumes/*")
		paths = append(paths, volumes...)
	}
	seen := make(map[string]bool)
	var roots []RootInfo
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		total, free, _ := diskUsage(path)
		label := path
		if path == home {
			label = "用户目录 · " + path
		}
		roots = append(roots, RootInfo{path, label, total, free})
	}
	return roots
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return fmt.Errorf("unsupported platform")
	}
}
