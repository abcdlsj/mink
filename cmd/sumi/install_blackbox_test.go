package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCleanBundleInstallForegroundRestartDoctorAndReinstallWithEmptyPATH(t *testing.T) {
	unified := buildUnifiedBinary(t)
	packager := buildPackageBinary(t)
	web := filepath.Join(t.TempDir(), "web")
	if err := os.Mkdir(web, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("Sumi"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "bundle")
	packageCommand := exec.Command(packager, "--version", "1.0.0", "--binary", unified, "--web", web, "--out", bundle)
	packageOutput, err := packageCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("package: %v\n%s", err, packageOutput)
	}
	homeDirectory := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(homeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	bundleBinary := filepath.Join(bundle, "bin", "sumi")
	environment := append(os.Environ(), "HOME="+homeDirectory, "PATH=")
	install := exec.Command(bundleBinary, "install", "--bundle", bundle)
	install.Env = environment
	installOutput, err := install.CombinedOutput()
	if err != nil {
		t.Fatalf("install: %v\n%s", err, installOutput)
	}
	dataRoot := filepath.Join(homeDirectory, ".sumi")
	assertFiveEntries(t, dataRoot)
	address := freeAddress(t)
	first := startCleanServer(t, bundleBinary, bundle, dataRoot, address, environment)
	stopCleanServer(t, first)
	second := startCleanServer(t, bundleBinary, bundle, dataRoot, address, environment)
	doctor := exec.Command(bundleBinary, "doctor", "--json")
	doctor.Env = environment
	doctorOutput, err := doctor.Output()
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, doctorOutput)
	}
	var report map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(doctorOutput), &report); err != nil || len(report) != 2 {
		t.Fatalf("doctor output = %q, %v", doctorOutput, err)
	}
	stopCleanServer(t, second)
	uninstall := exec.Command(bundleBinary, "uninstall")
	uninstall.Env = environment
	uninstallOutput, err := uninstall.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, uninstallOutput)
	}
	assertFiveEntries(t, dataRoot)
	reinstall := exec.Command(bundleBinary, "install", "--bundle", bundle)
	reinstall.Env = environment
	reinstallOutput, err := reinstall.CombinedOutput()
	if err != nil {
		t.Fatalf("reinstall: %v\n%s", err, reinstallOutput)
	}
	quiet := string(packageOutput) + string(installOutput) + string(doctorOutput) + string(uninstallOutput) + string(reinstallOutput)
	for _, private := range []string{homeDirectory, bundle, dataRoot} {
		if strings.Contains(quiet, private) {
			t.Fatalf("clean install output leaked private path: %q", quiet)
		}
	}
}

func buildPackageBinary(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve install blackbox source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binary := filepath.Join(t.TempDir(), "sumi-package")
	command := exec.Command("go", "build", "-o", binary, "./cmd/sumi-package")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sumi-package: %v\n%s", err, output)
	}
	return binary
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func startCleanServer(t *testing.T, binary, bundle, dataRoot, address string, environment []string) *exec.Cmd {
	t.Helper()
	command := exec.Command(binary, "server", "run", "--listen", address, "--data-root", dataRoot, "--web-root", filepath.Join(bundle, "web"))
	command.Env = environment
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+address+"/healthz", nil)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				return command
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = command.Process.Kill()
	_ = command.Wait()
	t.Fatalf("clean Server did not start: %s", output.String())
	return nil
}

func stopCleanServer(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("stop clean Server: %v", err)
	}
}

func assertFiveEntries(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != "agents,cache,config.toml,data,logs" {
		t.Fatalf("data root entries = %v", names)
	}
}
