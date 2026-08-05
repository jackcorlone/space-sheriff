package main

import (
	"testing"
	"time"
)

func TestAdviceProtectsSystemFile(t *testing.T) {
	now := time.Now()
	got := advise(`C:\Windows\System32\large.dll`, 100, now, now)
	if got.Level != "danger" {
		t.Fatalf("got %q, want danger", got.Level)
	}
	if got.RuleID != "SYSTEM_CORE" {
		t.Fatalf("got rule %q, want SYSTEM_CORE", got.RuleID)
	}
}

func TestAdviceProtectsSystemInstaller(t *testing.T) {
	now := time.Now()
	got := advise(`/System/Library/AssetsV2/com_apple_MobileAsset/example.dmg`, 100, now.Add(-90*24*time.Hour), now)
	if got.Level != "danger" || got.RuleID != "SYSTEM_PROTECTED" {
		t.Fatalf("unexpected advice: %+v", got)
	}
}

func TestAdviceAllowsOldCache(t *testing.T) {
	now := time.Now()
	got := advise(`/Users/me/Library/Caches/app/data.bin`, 100, now.Add(-40*24*time.Hour), now)
	if got.Level != "safe" {
		t.Fatalf("got %q, want safe", got.Level)
	}
	if got.RuleID != "CACHE_OLD" {
		t.Fatalf("got rule %q, want CACHE_OLD", got.RuleID)
	}
}

func TestAdviceProtectsPersonalFile(t *testing.T) {
	now := time.Now()
	got := advise(`/Users/me/Documents/report.pdf`, 100, now.Add(-400*24*time.Hour), now)
	if got.Level != "review" {
		t.Fatalf("got %q, want review", got.Level)
	}
}

func TestAdviceRecognizesOldInstallerInDownloads(t *testing.T) {
	now := time.Now()
	got := advise(`C:\Users\me\Downloads\setup.msi`, 100, now.Add(-60*24*time.Hour), now)
	if got.Level != "safe" || got.RuleID != "INSTALLER_OLD" {
		t.Fatalf("unexpected advice: %+v", got)
	}
}
