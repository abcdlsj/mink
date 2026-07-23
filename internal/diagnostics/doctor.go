package diagnostics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/abcdlsj/sumi/internal/configfile"
	"github.com/abcdlsj/sumi/internal/install"
	"github.com/abcdlsj/sumi/internal/osservice"
	"github.com/abcdlsj/sumi/internal/releasebundle"
	_ "modernc.org/sqlite"
)

type Result string

const (
	ResultOK      Result = "ok"
	ResultWarning Result = "warning"
	ResultError   Result = "error"
)

type Report struct {
	Code   string `json:"code"`
	Result Result `json:"result"`
}

type Services interface {
	Configure(string)
	Running(context.Context, osservice.Component) bool
}

type Doctor struct {
	Layout   install.Layout
	Services Services
	GOOS     string
	GOARCH   string
	Client   *http.Client
}

func New(dataRoot string) (*Doctor, error) {
	layout, err := install.Inspect(dataRoot)
	if err != nil {
		return nil, err
	}
	services, err := osservice.New()
	if err != nil {
		return nil, err
	}
	services.Configure(layout.DataRoot)
	return &Doctor{
		Layout: layout, Services: services, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Client: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (doctor *Doctor) Run(ctx context.Context) Report {
	if info, err := os.Lstat(doctor.Layout.RestoreRoot); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return Report{Code: "INSTALL_UNSAFE", Result: ResultError}
		}
		return Report{Code: "RESTORE_PENDING", Result: ResultError}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Report{Code: "INSTALL_UNSAFE", Result: ResultError}
	}
	dataRootInfo, err := os.Lstat(doctor.Layout.DataRoot)
	if err != nil || dataRootInfo.Mode()&os.ModeSymlink != 0 || !dataRootInfo.IsDir() {
		return Report{Code: "HOME_INVALID", Result: ResultError}
	}
	active, err := install.LoadActive(doctor.Layout)
	if err != nil {
		return Report{Code: "NOT_INSTALLED", Result: ResultError}
	}
	if _, err := configfile.Load(filepath.Join(doctor.Layout.DataRoot, "config.toml")); err != nil {
		return Report{Code: "CONFIG_INVALID", Result: ResultError}
	}
	if _, err := releasebundle.Open(doctor.Layout.VersionRoot(active.Release.ReleaseVersion), doctor.GOOS, doctor.GOARCH); err != nil {
		return Report{Code: "BUNDLE_INVALID", Result: ResultError}
	}
	serverRunning := doctor.Services.Running(ctx, osservice.Server)
	computerRunning := doctor.Services.Running(ctx, osservice.Computer)
	if !serverRunning || !computerRunning {
		return Report{Code: "SERVICE_STOPPED", Result: ResultWarning}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8080/healthz", nil)
	if err != nil {
		return Report{Code: "SERVER_UNHEALTHY", Result: ResultError}
	}
	response, err := doctor.Client.Do(request)
	if err != nil || response.StatusCode != http.StatusNoContent {
		if response != nil {
			response.Body.Close()
		}
		return Report{Code: "SERVER_UNHEALTHY", Result: ResultError}
	}
	response.Body.Close()
	paired, err := computerPaired(filepath.Join(doctor.Layout.DataRoot, "data", "computer", "state.db"))
	if err != nil {
		return Report{Code: "COMPUTER_STATE_INVALID", Result: ResultError}
	}
	if !paired {
		return Report{Code: "COMPUTER_UNPAIRED", Result: ResultWarning}
	}
	return Report{Code: "OK", Result: ResultOK}
}

func (report Report) JSON() ([]byte, error) {
	return json.Marshal(report)
}

func computerPaired(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("computer state is unsafe")
	}
	database, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer database.Close()
	var count int
	if err := database.QueryRow("SELECT count(*) FROM computer_identity").Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}
