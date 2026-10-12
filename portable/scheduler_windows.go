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
	if err != nil && !windowsTaskMissing(string(output)) {
		return fmt.Errorf("schtasks delete: %s", string(output))
	}
	return nil
}

func windowsTaskMissing(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "cannot find") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "not exist") ||
		strings.Contains(output, "找不到")
}

func (platformScheduleBackend) Inspect(schedule ScanSchedule, executable, dataDir string) (scheduleInspection, error) {
	output, err := exec.Command(
		"schtasks.exe", "/Query", "/TN", "SpaceSheriff-"+schedule.ID, "/XML",
	).CombinedOutput()
	if err != nil {
		if windowsTaskMissing(string(output)) {
			if schedule.Enabled {
				return scheduleInspection{State: "missing", Message: "Windows 计划任务不存在"}, nil
			}
			return scheduleInspection{State: "ok"}, nil
		}
		return scheduleInspection{State: "unknown"}, fmt.Errorf("schtasks query: %s", string(output))
	}
	if !schedule.Enabled {
		return scheduleInspection{State: "drifted", Message: "停用计划仍保留 Windows 计划任务"}, nil
	}
	normalized := strings.ToLower(strings.ReplaceAll(string(output), "\x00", ""))
	for _, value := range []string{
		strings.ToLower(executable), strings.ToLower(dataDir), strings.ToLower(schedule.ID),
	} {
		if value != "" && !strings.Contains(normalized, value) {
			return scheduleInspection{State: "drifted", Message: "Windows 计划任务参数已被修改"}, nil
		}
	}
	return scheduleInspection{State: "ok"}, nil
}

func (platformScheduleBackend) Name() string {
	return "Windows Task Scheduler"
}
