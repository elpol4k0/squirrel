package schedule

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

type Entry struct {
	Profile     string
	Schedule    string // cron expression
	BinaryPath  string // absolute path to squirrel binary
	ConfigPath  string
	Description string
}

func Install(e Entry) error {
	switch runtime.GOOS {
	case "linux":
		return installSystemd(e)
	case "darwin":
		return installLaunchd(e)
	case "windows":
		return installWindowsTask(e)
	default:
		return fmt.Errorf("unsupported platform %s; use your system scheduler with: %s run %s", runtime.GOOS, e.BinaryPath, e.Profile)
	}
}

func Remove(profileName string) error {
	switch runtime.GOOS {
	case "linux":
		return removeSystemd(profileName)
	case "darwin":
		return removeLaunchd(profileName)
	case "windows":
		return removeWindowsTask(profileName)
	default:
		return fmt.Errorf("unsupported platform")
	}
}

func List() ([]string, error) {
	switch runtime.GOOS {
	case "linux":
		return listSystemd()
	case "darwin":
		return listLaunchd()
	case "windows":
		return listWindowsTasks()
	default:
		return nil, fmt.Errorf("unsupported platform")
	}
}

const systemdServiceTmpl = `[Unit]
Description={{.Description}}
After=network.target

[Service]
Type=oneshot
ExecStart={{.BinaryPath}} run {{.Profile}} --config {{.ConfigPath}}
`

const systemdTimerTmpl = `[Unit]
Description={{.Description}} timer

[Timer]
OnCalendar={{.Schedule}}
Persistent=true

[Install]
WantedBy=timers.target
`

func systemdUnitDir() string {
	if os.Getuid() == 0 {
		return "/etc/systemd/system"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func installSystemd(e Entry) error {
	dir := systemdUnitDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	unitName := "squirrel-" + e.Profile

	svcPath := filepath.Join(dir, unitName+".service")
	if err := renderTemplate(svcPath, systemdServiceTmpl, e); err != nil {
		return err
	}
	timerPath := filepath.Join(dir, unitName+".timer")
	// Convert cron to OnCalendar. For simplicity accept "OnCalendar" expressions directly.
	e.Schedule = cronToSystemd(e.Schedule)
	if err := renderTemplate(timerPath, systemdTimerTmpl, e); err != nil {
		return err
	}

	cmd := "systemctl"
	args := []string{"enable", "--now", unitName + ".timer"}
	if os.Getuid() != 0 {
		args = append([]string{"--user"}, args...)
	}
	out, err := exec.Command(cmd, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl enable: %w\n%s", err, out)
	}
	fmt.Printf("installed: %s.timer\n", unitName)
	return nil
}

func removeSystemd(profileName string) error {
	unitName := "squirrel-" + profileName
	args := []string{"disable", "--now", unitName + ".timer"}
	if os.Getuid() != 0 {
		args = append([]string{"--user"}, args...)
	}
	exec.Command("systemctl", args...).Run() //nolint:errcheck

	dir := systemdUnitDir()
	os.Remove(filepath.Join(dir, unitName+".service"))
	os.Remove(filepath.Join(dir, unitName+".timer"))
	fmt.Printf("removed: %s\n", unitName)
	return nil
}

func listSystemd() ([]string, error) {
	args := []string{"list-timers", "--no-legend", "squirrel-*"}
	if os.Getuid() != 0 {
		args = append([]string{"--user"}, args...)
	}
	out, err := exec.Command("systemctl", args...).Output()
	if err != nil {
		return nil, nil
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) > 0 {
			names = append(names, strings.TrimSuffix(parts[len(parts)-1], ".timer"))
		}
	}
	return names, nil
}

// @-shortcuts → systemd OnCalendar; everything else passes through unchanged
func cronToSystemd(cron string) string {
	switch cron {
	case "@hourly":
		return "hourly"
	case "@daily", "@midnight":
		return "daily"
	case "@weekly":
		return "weekly"
	case "@monthly":
		return "monthly"
	case "@yearly", "@annually":
		return "yearly"
	}
	return cron
}

const launchdTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>io.squirrel.{{.Profile}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.BinaryPath}}</string>
    <string>run</string>
    <string>{{.Profile}}</string>
    <string>--config</string>
    <string>{{.ConfigPath}}</string>
  </array>
  <key>StartCalendarInterval</key>
  {{cronToLaunchd .Schedule}}
  <key>RunAtLoad</key>
  <false/>
</dict>
</plist>
`

func launchdAgentsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

func launchdPlistName(profileName string) string {
	return "io.squirrel." + profileName + ".plist"
}

func installLaunchd(e Entry) error {
	dir := launchdAgentsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	plistPath := filepath.Join(dir, launchdPlistName(e.Profile))
	content := buildLaunchdPlist(e)
	if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
		return err
	}
	out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl load: %w\n%s", err, out)
	}
	fmt.Printf("installed: %s\n", plistPath)
	return nil
}

func removeLaunchd(profileName string) error {
	plistPath := filepath.Join(launchdAgentsDir(), launchdPlistName(profileName))
	exec.Command("launchctl", "unload", plistPath).Run() //nolint:errcheck
	os.Remove(plistPath)
	fmt.Printf("removed: %s\n", plistPath)
	return nil
}

func listLaunchd() ([]string, error) {
	dir := launchdAgentsDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "io.squirrel.") && strings.HasSuffix(n, ".plist") {
			names = append(names, strings.TrimSuffix(strings.TrimPrefix(n, "io.squirrel."), ".plist"))
		}
	}
	return names, nil
}

func buildLaunchdPlist(e Entry) string {
	// parse minimal cron: "minute hour * * *"
	parts := strings.Fields(e.Schedule)
	minute, hour := "0", "0"
	if len(parts) >= 2 {
		if parts[0] != "*" {
			minute = parts[0]
		}
		if parts[1] != "*" {
			hour = parts[1]
		}
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>io.squirrel.%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>run</string>
    <string>%s</string>
    <string>--config</string>
    <string>%s</string>
  </array>
  <key>StartCalendarInterval</key>
  <dict>
    <key>Minute</key>
    <integer>%s</integer>
    <key>Hour</key>
    <integer>%s</integer>
  </dict>
  <key>RunAtLoad</key>
  <false/>
</dict>
</plist>`, e.Profile, e.BinaryPath, e.Profile, e.ConfigPath, minute, hour)
}

func taskName(profileName string) string {
	return "squirrel-" + profileName
}

func installWindowsTask(e Entry) error {
	name := taskName(e.Profile)
	trigger := cronToSchtasks(e.Schedule)
	args := []string{
		"/Create", "/F",
		"/TN", name,
		"/TR", fmt.Sprintf(`"%s" run %s --config "%s"`, e.BinaryPath, e.Profile, e.ConfigPath),
		"/SC", trigger,
	}
	out, err := exec.Command("schtasks", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks: %w\n%s", err, out)
	}
	fmt.Printf("installed Windows task: %s\n", name)
	return nil
}

func removeWindowsTask(profileName string) error {
	out, err := exec.Command("schtasks", "/Delete", "/F", "/TN", taskName(profileName)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks delete: %w\n%s", err, out)
	}
	fmt.Printf("removed Windows task: %s\n", taskName(profileName))
	return nil
}

func listWindowsTasks() ([]string, error) {
	out, err := exec.Command("schtasks", "/Query", "/FO", "LIST").Output()
	if err != nil {
		return nil, nil
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "TaskName:") && strings.Contains(line, "squirrel-") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "TaskName:"))
			names = append(names, name)
		}
	}
	return names, nil
}

func cronToSchtasks(cron string) string {
	switch cron {
	case "@hourly":
		return "HOURLY"
	case "@daily", "@midnight":
		return "DAILY"
	case "@weekly":
		return "WEEKLY"
	case "@monthly":
		return "MONTHLY"
	}
	return "DAILY"
}

func renderTemplate(path, tmplStr string, data interface{}) error {
	tmpl, err := template.New("").Parse(tmplStr)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, data)
}
