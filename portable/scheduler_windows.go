//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type platformScheduleBackend struct{}

var windowsWeekdays = []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}

func windowsCommandLine(arguments []string) string {
	quoted := make([]string, len(arguments))
	for index, argument := range arguments {
		if argument != "" && !strings.ContainsAny(argument, " \t\"") {
			quoted[index] = argument
			continue
		}
		var value strings.Builder
		value.WriteByte('"')
		backslashes := 0
		for _, character := range argument {
			if character == '\\' {
				backslashes++
				continue
			}
			if character == '"' {
				value.WriteString(strings.Repeat("\\", backslashes*2+1))
				value.WriteRune(character)
				backslashes = 0
				continue
			}
			value.WriteString(strings.Repeat("\\", backslashes))
			backslashes = 0
			value.WriteRune(character)
		}
		value.WriteString(strings.Repeat("\\", backslashes*2))
		value.WriteByte('"')
		quoted[index] = value.String()
	}
	return strings.Join(quoted, " ")
}

func windowsScheduleArguments(schedule ScanSchedule, executable, dataDir string) []string {
	arguments := []string{
		"/Create", "/TN", "SpaceSheriff-" + schedule.ID,
		"/TR", windowsCommandLine([]string{
			executable, "--scheduled-scan", schedule.ID, "--data-dir", dataDir,
		}),
		"/ST", fmt.Sprintf("%02d:%02d", schedule.Hour, schedule.Minute), "/F",
	}
	if schedule.Cadence == "weekly" {
		return append(arguments, "/SC", "WEEKLY", "/D", windowsWeekdays[schedule.Weekday])
	}
	return append(arguments, "/SC", "DAILY")
}

func (platformScheduleBackend) Install(schedule ScanSchedule, executable, dataDir string) error {
	output, err := exec.Command(
		"schtasks.exe", windowsScheduleArguments(schedule, executable, dataDir)...,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks create (%s): %s", strconv.Itoa(int(schedule.Weekday)), string(output))
	}
	return nil
}

func (platformScheduleBackend) Remove(id string) error {
	output, err := exec.Command(
		"schtasks.exe", "/Delete", "/TN", "SpaceSheriff-"+id, "/F",
	).CombinedOutput()
	lower := strings.ToLower(string(output))
	missing := strings.Contains(lower, "cannot find") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "not exist") ||
		strings.Contains(string(output), "找不到")
	if err != nil && !missing {
		return fmt.Errorf("schtasks delete: %s", string(output))
	}
	return nil
}

func (platformScheduleBackend) Name() string {
	return "Windows Task Scheduler"
}
