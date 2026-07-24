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

func (d *Doctor) Run(ctx context.Context) Report {
	if info, err := os.Lstat(d.Layout.RestoreRoot); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return Report{Code: "INSTALL_UNSAFE", Result: ResultError}
		}
		return Report{Code: "RESTORE_PENDING", Result: ResultError}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Report{Code: "INSTALL_UNSAFE", Result: ResultError}
	}
	dirInfo, err := os.Lstat(d.Layout.DataRoot)
	if err != nil || dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return Report{Code: "HOME_INVALID", Result: ResultError}
	}
	active, err := install.LoadActive(d.Layout)
	if err != nil {
		return Report{Code: "NOT_INSTALLED", Result: ResultError}
	}
	if _, err := configfile.Load(filepath.Join(d.Layout.DataRoot, "config.toml")); err != nil {
		return Report{Code: "CONFIG_INVALID", Result: ResultError}
	}
	if _, err := releasebundle.Open(d.Layout.VersionRoot(active.Release.ReleaseVersion), d.GOOS, d.GOARCH); err != nil {
		return Report{Code: "BUNDLE_INVALID", Result: ResultError}
	}
	svr := d.Services.Running(ctx, osservice.Server)
	cmptr := d.Services.Running(ctx, osservice.Computer)
	if !svr || !cmptr {
		return Report{Code: "SERVICE_STOPPED", Result: ResultWarning}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8080/healthz", nil)
	if err != nil {
		return Report{Code: "SERVER_UNHEALTHY", Result: ResultError}
	}
	resp, err := d.Client.Do(req)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		if resp != nil {
			resp.Body.Close()
		}
		return Report{Code: "SERVER_UNHEALTHY", Result: ResultError}
	}
	resp.Body.Close()
	paired, err := computerPaired(filepath.Join(d.Layout.DataRoot, "data", "computer", "state.db"))
	if err != nil {
		return Report{Code: "COMPUTER_STATE_INVALID", Result: ResultError}
	}
	if !paired {
		return Report{Code: "COMPUTER_UNPAIRED", Result: ResultWarning}
	}
	return Report{Code: "OK", Result: ResultOK}
}

func (r Report) JSON() ([]byte, error) {
	return json.Marshal(r)
}

func computerPaired(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("computer state is unsafe")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM computer_identity").Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}
