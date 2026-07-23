package osservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if executable == "/bin/launchctl" && len(args) > 0 && args[0] == "print" {
		return errLaunchdNotLoaded
	}
	if runner.fail {
		return os.ErrNotExist
	}
	return nil
}

type launchdRestartRunner struct {
	commands       []recordedCommand
	loadedChecks   int
	loadedUntil    int
	bootoutError   error
	bootstrapError error
	probeError     error
}

func (runner *launchdRestartRunner) Run(_ context.Context, executable string, args ...string) error {
	runner.commands = append(runner.commands, recordedCommand{executable: executable, args: append([]string(nil), args...)})
	if executable != "/bin/launchctl" || len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "print":
		runner.loadedChecks++
		if runner.loadedChecks <= runner.loadedUntil {
			return nil
		}
		if runner.probeError != nil {
			return runner.probeError
		}
		return errLaunchdNotLoaded
	case "bootout":
		return runner.bootoutError
	case "bootstrap":
		return runner.bootstrapError
	default:
		return nil
	}
}

type virtualClock struct {
	now   time.Time
	waits []time.Duration
}

func (clock *virtualClock) wait(ctx context.Context, delay time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		clock.waits = append(clock.waits, delay)
		clock.now = clock.now.Add(delay)
		return nil
	}
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

func TestDarwinRestartWaitsWhileInactiveJobRemainsLoadedBeforeBootstrap(t *testing.T) {
	runner := &launchdRestartRunner{loadedUntil: 2}
	manager, clock := newDarwinRestartManager(t, runner)
	if err := manager.Restart(context.Background(), Server); err != nil {
		t.Fatal(err)
	}
	if len(clock.waits) != 1 || clock.waits[0] != 25*time.Millisecond {
		t.Fatalf("restart waits = %v", clock.waits)
	}
	assertRecordedCommand(t, runner.commands, "/bin/launchctl", "bootout", manager.domainTarget(Server))
	assertRecordedCommand(t, runner.commands, "/bin/launchctl", "bootstrap", "gui/501", manager.unitPath(Server))
	assertRecordedCommand(t, runner.commands, "/bin/launchctl", "kickstart", "-k", manager.domainTarget(Server))
}

func TestDarwinRestartFailsClosedWhenLoadedProbeFails(t *testing.T) {
	probeError := errors.New("launchd probe failed")
	runner := &launchdRestartRunner{loadedUntil: 1, probeError: probeError}
	manager, _ := newDarwinRestartManager(t, runner)
	err := manager.Restart(context.Background(), Server)
	if !errors.Is(err, probeError) {
		t.Fatalf("restart error = %v", err)
	}
	assertCommandNotRecorded(t, runner.commands, "/bin/launchctl", "bootstrap", "gui/501", manager.unitPath(Server))
	assertCommandNotRecorded(t, runner.commands, "/bin/launchctl", "kickstart", "-k", manager.domainTarget(Server))
}

func TestDarwinRestartReturnsBootoutFailureWithoutRemovalProbeOrStart(t *testing.T) {
	bootoutError := errors.New("launchd bootout failed")
	runner := &launchdRestartRunner{loadedUntil: 1, bootoutError: bootoutError}
	manager, clock := newDarwinRestartManager(t, runner)
	err := manager.Restart(context.Background(), Server)
	if !errors.Is(err, bootoutError) {
		t.Fatalf("restart error = %v", err)
	}
	if runner.loadedChecks != 1 {
		t.Fatalf("loaded checks = %d", runner.loadedChecks)
	}
	if len(clock.waits) != 0 {
		t.Fatalf("restart waits = %v", clock.waits)
	}
	want := []recordedCommand{
		{executable: "/bin/launchctl", args: []string{"print", manager.domainTarget(Server)}},
		{executable: "/bin/launchctl", args: []string{"bootout", manager.domainTarget(Server)}},
	}
	if len(runner.commands) != len(want) {
		t.Fatalf("commands = %#v", runner.commands)
	}
	for index := range want {
		if runner.commands[index].executable != want[index].executable ||
			strings.Join(runner.commands[index].args, "\x00") != strings.Join(want[index].args, "\x00") {
			t.Fatalf("command %d = %#v, want %#v", index, runner.commands[index], want[index])
		}
	}
}

func TestDarwinRestartStartsWhenServiceIsAlreadyAbsent(t *testing.T) {
	runner := &launchdRestartRunner{}
	manager, clock := newDarwinRestartManager(t, runner)
	if err := manager.Restart(context.Background(), Server); err != nil {
		t.Fatal(err)
	}
	if len(clock.waits) != 0 {
		t.Fatalf("restart waits = %v", clock.waits)
	}
	assertCommandNotRecorded(t, runner.commands, "/bin/launchctl", "bootout", manager.domainTarget(Server))
	assertRecordedCommand(t, runner.commands, "/bin/launchctl", "bootstrap", "gui/501", manager.unitPath(Server))
	assertRecordedCommand(t, runner.commands, "/bin/launchctl", "kickstart", "-k", manager.domainTarget(Server))
}

func TestDarwinRestartTimesOutWithoutBootstrapWhileServiceRemainsLoaded(t *testing.T) {
	runner := &launchdRestartRunner{loadedUntil: 100}
	manager, clock := newDarwinRestartManager(t, runner)
	manager.launchdRemovalTimeout = 100 * time.Millisecond
	err := manager.Restart(context.Background(), Server)
	if err == nil || err.Error() != "current-user service removal timed out" {
		t.Fatalf("restart error = %v", err)
	}
	if len(clock.waits) != 4 || clock.now.Sub(time.Unix(0, 0)) != 100*time.Millisecond {
		t.Fatalf("restart waits = %v, now = %v", clock.waits, clock.now)
	}
	assertCommandNotRecorded(t, runner.commands, "/bin/launchctl", "bootstrap", "gui/501", manager.unitPath(Server))
	assertCommandNotRecorded(t, runner.commands, "/bin/launchctl", "kickstart", "-k", manager.domainTarget(Server))
}

func TestDarwinRestartReturnsStartFailureAfterRemoval(t *testing.T) {
	startError := errors.New("bootstrap failed")
	runner := &launchdRestartRunner{bootstrapError: startError}
	manager, _ := newDarwinRestartManager(t, runner)
	err := manager.Restart(context.Background(), Server)
	if !errors.Is(err, startError) {
		t.Fatalf("restart error = %v", err)
	}
	assertCommandNotRecorded(t, runner.commands, "/bin/launchctl", "kickstart", "-k", manager.domainTarget(Server))
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

func newDarwinRestartManager(t *testing.T, runner Runner) (*Manager, *virtualClock) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager("darwin", home, 501, runner)
	if err != nil {
		t.Fatal(err)
	}
	manager.Configure(filepath.Join(home, ".sumi"))
	clock := &virtualClock{now: time.Unix(0, 0)}
	manager.now = func() time.Time { return clock.now }
	manager.wait = clock.wait
	return manager, clock
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

func assertCommandNotRecorded(t *testing.T, commands []recordedCommand, executable string, arguments ...string) {
	t.Helper()
	for _, command := range commands {
		if command.executable == executable && strings.Join(command.args, "\x00") == strings.Join(arguments, "\x00") {
			t.Fatalf("command unexpectedly recorded: %s %v", executable, arguments)
		}
	}
}
