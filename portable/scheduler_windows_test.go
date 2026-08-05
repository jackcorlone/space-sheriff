//go:build windows

package main

import (
	"slices"
	"strings"
	"testing"
)

func TestWindowsScheduleArgumentsQuotePathsAndWeekday(t *testing.T) {
	schedule := validTestSchedule(`C:\Users\Test User`)
	schedule.ID = "abc123"
	arguments := windowsScheduleArguments(
		schedule, `C:\Program Files\Space Sheriff\SpaceSheriff.exe`,
		`C:\Users\Test User\AppData\Space Sheriff`,
	)
	if !slices.Contains(arguments, "SAT") {
		t.Fatalf("missing weekday: %#v", arguments)
	}
	taskCommand := arguments[slices.Index(arguments, "/TR")+1]
	if !strings.Contains(taskCommand, `"C:\Program Files\Space Sheriff\SpaceSheriff.exe"`) ||
		!strings.Contains(taskCommand, `"C:\Users\Test User\AppData\Space Sheriff"`) {
		t.Fatalf("paths were not quoted: %s", taskCommand)
	}
}

func TestWindowsTaskMissingMessages(t *testing.T) {
	for _, message := range []string{
		"ERROR: The system cannot find the file specified.",
		"Task does not exist",
		"找不到指定的任务",
	} {
		if !windowsTaskMissing(message) {
			t.Fatalf("missing task message not recognized: %q", message)
		}
	}
	if windowsTaskMissing("Access is denied") {
		t.Fatal("permission error was treated as a missing task")
	}
}
