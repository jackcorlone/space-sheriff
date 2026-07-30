//go:build darwin

package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchAgentDataUsesArgumentArrayAndCalendar(t *testing.T) {
	schedule := validTestSchedule("/tmp")
	schedule.ID = "abc123"
	data, err := launchAgentData(
		schedule, "/Applications/Space Sheriff & Tools", "/tmp/Space Sheriff",
	)
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	for _, expected := range []string{
		"<key>ProgramArguments</key>",
		"<string>/Applications/Space Sheriff &amp; Tools</string>",
		"<key>Weekday</key>",
		"<integer>6</integer>",
		"<string>--scheduled-scan</string>",
		"<string>abc123</string>",
	} {
		if !strings.Contains(document, expected) {
			t.Fatalf("plist missing %q:\n%s", expected, document)
		}
	}
	command := exec.Command("plutil", "-lint", "-")
	command.Stdin = bytes.NewReader(data)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("invalid plist: %v\n%s", err, output)
	}
}

func TestLaunchAgentBackendMetadata(t *testing.T) {
	path, err := launchAgentPath("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "com.spacesheriff.scan.abc123.plist" {
		t.Fatalf("unexpected launch agent path: %s", path)
	}
	if !strings.HasPrefix(launchDomain(), "gui/") {
		t.Fatalf("unexpected launch domain: %s", launchDomain())
	}
	if (platformScheduleBackend{}).Name() != "macOS LaunchAgent" {
		t.Fatal("unexpected backend name")
	}
}

func TestLaunchctlMissingServiceMessages(t *testing.T) {
	for _, message := range []string{
		"Could not find specified service",
		"Boot-out failed: 3: No such process",
		"service not found",
	} {
		if !launchctlServiceMissing(message) {
			t.Fatalf("missing service message not recognized: %q", message)
		}
	}
	if launchctlServiceMissing("Operation not permitted") {
		t.Fatal("permission error was treated as a missing service")
	}
}
