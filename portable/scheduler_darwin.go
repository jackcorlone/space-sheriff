//go:build darwin

package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type platformScheduleBackend struct{}

type launchAgentPlist struct {
	XMLName xml.Name        `xml:"plist"`
	Version string          `xml:"version,attr"`
	Dict    launchAgentDict `xml:"dict"`
}

type launchAgentDict struct {
	Label      string
	Arguments  []string
	Schedule   ScanSchedule
	StdoutPath string
	StderrPath string
}

func (d launchAgentDict) MarshalXML(encoder *xml.Encoder, start xml.StartElement) error {
	start.Name.Local = "dict"
	if err := encoder.EncodeToken(start); err != nil {
		return err
	}
	keyString := func(key, value string) error {
		if err := encoder.EncodeElement(key, xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
			return err
		}
		return encoder.EncodeElement(value, xml.StartElement{Name: xml.Name{Local: "string"}})
	}
	if err := keyString("Label", "com.spacesheriff.scan."+d.Label); err != nil {
		return err
	}
	if err := encoder.EncodeElement("ProgramArguments", xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
		return err
	}
	array := xml.StartElement{Name: xml.Name{Local: "array"}}
	if err := encoder.EncodeToken(array); err != nil {
		return err
	}
	for _, argument := range d.Arguments {
		if err := encoder.EncodeElement(argument, xml.StartElement{Name: xml.Name{Local: "string"}}); err != nil {
			return err
		}
	}
	if err := encoder.EncodeToken(array.End()); err != nil {
		return err
	}
	if err := encoder.EncodeElement("StartCalendarInterval", xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
		return err
	}
	calendar := xml.StartElement{Name: xml.Name{Local: "dict"}}
	if err := encoder.EncodeToken(calendar); err != nil {
		return err
	}
	values := []struct {
		key   string
		value int
	}{{"Hour", d.Schedule.Hour}, {"Minute", d.Schedule.Minute}}
	if d.Schedule.Cadence == "weekly" {
		values = append(values, struct {
			key   string
			value int
		}{"Weekday", d.Schedule.Weekday})
	}
	for _, item := range values {
		if err := encoder.EncodeElement(item.key, xml.StartElement{Name: xml.Name{Local: "key"}}); err != nil {
			return err
		}
		if err := encoder.EncodeElement(strconv.Itoa(item.value), xml.StartElement{Name: xml.Name{Local: "integer"}}); err != nil {
			return err
		}
	}
	if err := encoder.EncodeToken(calendar.End()); err != nil {
		return err
	}
	if err := keyString("StandardOutPath", d.StdoutPath); err != nil {
		return err
	}
	if err := keyString("StandardErrorPath", d.StderrPath); err != nil {
		return err
	}
	if err := keyString("ProcessType", "Background"); err != nil {
		return err
	}
	return encoder.EncodeToken(start.End())
}

func launchAgentData(schedule ScanSchedule, executable, dataDir string) ([]byte, error) {
	logDir := filepath.Join(dataDir, "logs")
	document := launchAgentPlist{
		Version: "1.0",
		Dict: launchAgentDict{
			Label: schedule.ID,
			Arguments: []string{
				executable, "--scheduled-scan", schedule.ID, "--data-dir", dataDir,
			},
			Schedule:   schedule,
			StdoutPath: filepath.Join(logDir, "scheduled-"+schedule.ID+".log"),
			StderrPath: filepath.Join(logDir, "scheduled-"+schedule.ID+".error.log"),
		},
	}
	data, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header+"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n"), data...), nil
}

func launchAgentPath(id string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", "com.spacesheriff.scan."+id+".plist"), nil
}

func launchDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func (platformScheduleBackend) Install(schedule ScanSchedule, executable, dataDir string) error {
	path, err := launchAgentPath(schedule.ID)
	if err != nil {
		return err
	}
	data, err := launchAgentData(schedule, executable, dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o700); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "bootout", launchDomain()+"/com.spacesheriff.scan."+schedule.ID).Run()
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if output, err := exec.Command("launchctl", "bootstrap", launchDomain(), path).CombinedOutput(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("launchctl bootstrap: %s", string(output))
	}
	return nil
}

func (platformScheduleBackend) Remove(id string) error {
	path, err := launchAgentPath(id)
	if err != nil {
		return err
	}
	output, commandErr := exec.Command(
		"launchctl", "bootout", launchDomain()+"/com.spacesheriff.scan."+id,
	).CombinedOutput()
	if commandErr != nil && !launchctlServiceMissing(string(output)) {
		return fmt.Errorf("launchctl bootout: %s", string(output))
	}
	removeErr := os.Remove(path)
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	return nil
}

func launchctlServiceMissing(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "could not find") ||
		strings.Contains(lower, "no such process") ||
		strings.Contains(lower, "not found")
}

func (platformScheduleBackend) Name() string {
	return "macOS LaunchAgent"
}
