package osservice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordedCommand struct {
	executable string
	args       []string
}

type recordingRunner struct {
	commands []recordedCommand
	fail     bool
}

func (runner *recordingRunner) Run(_ context.Context, executable string, args ...string) error {
	runner.commands = append(runner.commands, recordedCommand{executable: executable, args: append([]string(nil), args...)})
	if runner.fail {
		return os.ErrNotExist
	}
	return nil
}

func TestCurrentUserServiceCommandOraclesContainNoSudo(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			if err := os.Mkdir(home, 0o700); err != nil {
				t.Fatal(err)
			}
			runner := &recordingRunner{}
			manager, err := NewManager(goos, home, 501, runner)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(home, ".sumi")
			config := InstallConfig{Binary: filepath.Join(home, "bin", "sumi"), WebRoot: filepath.Join(home, "web"), DataRoot: root}
			if err := manager.Install(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			for _, component := range []Component{Server, Computer} {
				if err := manager.Start(context.Background(), component); err != nil {
					t.Fatal(err)
				}
				if err := manager.Restart(context.Background(), component); err != nil {
					t.Fatal(err)
				}
				if err := manager.Stop(context.Background(), component); err != nil {
					t.Fatal(err)
				}
			}
			for _, command := range runner.commands {
				joined := command.executable + " " + strings.Join(command.args, " ")
				if strings.Contains(joined, "sudo") || strings.Contains(joined, "systemctl --system") {
					t.Fatalf("system-scope command = %q", joined)
				}
			}
			for _, path := range manager.UnitPaths() {
				payload, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				text := string(payload)
				if !strings.Contains(text, config.Binary) || !strings.Contains(text, "run") || strings.Contains(text, "sudo") {
					t.Fatalf("unit %q = %q", path, text)
				}
			}
		})
	}
}

func TestServiceRejectsRootAndSymlinkUnit(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if _, err := NewManager("darwin", home, 0, &recordingRunner{}); err == nil {
		t.Fatal("root service scope was accepted")
	}
	if err := os.MkdirAll(filepath.Join(home, "Library"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(home, "Library", "LaunchAgents")); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager("darwin", home, 501, &recordingRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Install(context.Background(), InstallConfig{Binary: "/tmp/sumi", WebRoot: "/tmp/web", DataRoot: "/tmp/data"}); err == nil {
		t.Fatal("symlink unit directory was accepted")
	}
}

func TestSystemdInstallEnablesAndUninstallDisablesUserUnits(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	manager, err := NewManager("linux", home, 501, runner)
	if err != nil {
		t.Fatal(err)
	}
	config := InstallConfig{Binary: filepath.Join(home, "bin", "sumi"), WebRoot: filepath.Join(home, "web"), DataRoot: filepath.Join(home, ".sumi")}
	if err := manager.Install(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, component := range []Component{Server, Computer} {
		unit := manager.unitName(component)
		assertRecordedCommand(t, runner.commands, "/usr/bin/systemctl", "--user", "enable", unit)
		assertRecordedCommand(t, runner.commands, "/usr/bin/systemctl", "--user", "disable", unit)
	}
}

func assertRecordedCommand(t *testing.T, commands []recordedCommand, executable string, arguments ...string) {
	t.Helper()
	for _, command := range commands {
		if command.executable == executable && strings.Join(command.args, "\x00") == strings.Join(arguments, "\x00") {
			return
		}
	}
	t.Fatalf("command not recorded: %s %v", executable, arguments)
}
