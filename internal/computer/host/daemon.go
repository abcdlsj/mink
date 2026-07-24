package host

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/run/v1/runv1connect"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/credential"
	"github.com/abcdlsj/sumi/internal/observability"
)

type Execution = computerruntime.Execution
type Completion = computerruntime.Completion

type DaemonConfig struct {
	ServerURL           string
	DataRoot            string
	State               *computerstate.State
	HTTPClient          *http.Client
	RuntimeSupervisor   *computerruntime.Supervisor
	CredentialManager   *credential.Manager
	CapabilityInventory *computerv1.CapabilityInventoryDeclaration
	HeartbeatInterval   time.Duration
	SnapshotInterval    time.Duration
	RunInterval         time.Duration
	RunRenewInterval    time.Duration
	OutboxInterval      time.Duration
	RuntimeRenewBefore  time.Duration
	RPCDeadline         time.Duration
	BackoffMax          time.Duration
	Now                 func() time.Time
	RetryJitter         func(time.Duration) time.Duration
	Logger              *observability.Logger
}

type computerDaemonClient interface {
	HeartbeatComputer(context.Context, *connect.Request[computerv1.HeartbeatComputerRequest]) (*connect.Response[computerv1.HeartbeatComputerResponse], error)
	ClaimCredentialDelivery(context.Context, *connect.Request[computerv1.ClaimCredentialDeliveryRequest]) (*connect.Response[computerv1.ClaimCredentialDeliveryResponse], error)
	CompleteCredentialDelivery(context.Context, *connect.Request[computerv1.CompleteCredentialDeliveryRequest]) (*connect.Response[computerv1.CompleteCredentialDeliveryResponse], error)
}

type placementDaemonClient interface {
	ListComputerPlacements(context.Context, *connect.Request[placementv1.ListComputerPlacementsRequest]) (*connect.Response[placementv1.ListComputerPlacementsResponse], error)
	AcknowledgeAgentPlacement(context.Context, *connect.Request[placementv1.AcknowledgeAgentPlacementRequest]) (*connect.Response[placementv1.AcknowledgeAgentPlacementResponse], error)
}

type runtimeDaemonClient interface {
	CreateAgentRuntimeSession(context.Context, *connect.Request[runtimev1.CreateAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.CreateAgentRuntimeSessionResponse], error)
	RenewAgentRuntimeSession(context.Context, *connect.Request[runtimev1.RenewAgentRuntimeSessionRequest]) (*connect.Response[runtimev1.RenewAgentRuntimeSessionResponse], error)
}

type inboxDaemonClient interface {
	ObserveTarget(context.Context, *connect.Request[inboxv1.ObserveTargetRequest]) (*connect.Response[inboxv1.ObserveTargetResponse], error)
}

type runDaemonClient interface {
	ListRuns(context.Context, *connect.Request[runv1.ListRunsRequest]) (*connect.Response[runv1.ListRunsResponse], error)
	GetRun(context.Context, *connect.Request[runv1.GetRunRequest]) (*connect.Response[runv1.GetRunResponse], error)
	ClaimRun(context.Context, *connect.Request[runv1.ClaimRunRequest]) (*connect.Response[runv1.ClaimRunResponse], error)
	RenewRun(context.Context, *connect.Request[runv1.RenewRunRequest]) (*connect.Response[runv1.RenewRunResponse], error)
	CancelRun(context.Context, *connect.Request[runv1.CancelRunRequest]) (*connect.Response[runv1.CancelRunResponse], error)
	CompleteRun(context.Context, *connect.Request[runv1.CompleteRunRequest]) (*connect.Response[runv1.CompleteRunResponse], error)
}

type Daemon struct {
	config     DaemonConfig
	computers  computerDaemonClient
	placements placementDaemonClient
	runtimes   runtimeDaemonClient
	inbox      inboxDaemonClient
	runs       runDaemonClient

	workers sync.WaitGroup

	persistOutbox func(context.Context, computerstate.OutboxEvent) error

	lifecycleLogger *observability.Logger
	transportLogger *observability.Logger
	placementLogger *observability.Logger
	runtimeLogger   *observability.Logger
	runLogger       *observability.Logger
	engineLogger    *observability.Logger
	outboxLogger    *observability.Logger
}

func NewDaemon(config DaemonConfig) *Daemon {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	setDaemonDefaults(&config)
	return &Daemon{
		config:          config,
		computers:       computerv1connect.NewComputerServiceClient(client, config.ServerURL),
		placements:      placementv1connect.NewPlacementServiceClient(client, config.ServerURL),
		runtimes:        runtimev1connect.NewAgentRuntimeServiceClient(client, config.ServerURL),
		inbox:           inboxv1connect.NewInboxServiceClient(client, config.ServerURL),
		runs:            runv1connect.NewRunServiceClient(client, config.ServerURL),
		lifecycleLogger: observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryLifecycle),
		transportLogger: observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryTransport),
		placementLogger: observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryPlacement),
		runtimeLogger:   observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryRuntime),
		runLogger:       observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryRun),
		engineLogger:    observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryEngine),
		outboxLogger:    observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryOutbox),
	}
}

func (d *Daemon) Run(ctx context.Context) error {
	if d.config.State == nil {
		return errors.New("computer state is required")
	}
	if d.config.RuntimeSupervisor == nil {
		return errors.New("runtime supervisor is required")
	}
	identity, found, err := d.config.State.Identity(ctx)
	if err != nil {
		return err
	}
	if !found || identity.ServerURL != d.config.ServerURL {
		return errors.New("computer identity is unavailable for this Server")
	}
	d.lifecycleLogger.Info("computer daemon started", "event", "computer.daemon.started", "computer_id", identity.ComputerID, "server_origin", identity.ServerURL)
	var loops sync.WaitGroup
	loopCount := 3
	if d.config.CredentialManager != nil {
		loopCount++
	}
	loops.Add(loopCount)
	go func() { defer loops.Done(); d.connectivitySupervisor(ctx, identity) }()
	go func() { defer loops.Done(); d.runLoop(ctx, identity) }()
	go func() { defer loops.Done(); d.outboxLoop(ctx) }()
	if d.config.CredentialManager != nil {
		go func() { defer loops.Done(); d.credentialLoop(ctx, identity) }()
	}
	<-ctx.Done()
	d.lifecycleLogger.Info("computer daemon shutdown requested", "event", "computer.daemon.shutdown.requested", "reason", context.Cause(ctx))
	d.config.RuntimeSupervisor.Close()
	loops.Wait()
	d.workers.Wait()
	d.lifecycleLogger.Info("computer daemon stopped", "event", "computer.daemon.stopped", "computer_id", identity.ComputerID)
	return nil
}

func (d *Daemon) rpcContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d.config.RPCDeadline)
}

func setDaemonDefaults(config *DaemonConfig) {
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 10 * time.Second
	}
	if config.SnapshotInterval <= 0 {
		config.SnapshotInterval = 10 * time.Second
	}
	if config.RunInterval <= 0 {
		config.RunInterval = time.Second
	}
	if config.RunRenewInterval <= 0 {
		config.RunRenewInterval = 20 * time.Second
	}
	if config.OutboxInterval <= 0 {
		config.OutboxInterval = time.Second
	}
	if config.RuntimeRenewBefore <= 0 {
		config.RuntimeRenewBefore = 2 * time.Minute
	}
	if config.RPCDeadline <= 0 {
		config.RPCDeadline = 5 * time.Second
	}
	if config.BackoffMax <= 0 {
		config.BackoffMax = 30 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.RetryJitter == nil {
		config.RetryJitter = func(window time.Duration) time.Duration {
			if window <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(window) + 1))
		}
	}
}

func retryDelay(failures uint, base, maximum time.Duration, jitter func(time.Duration) time.Duration) time.Duration {
	delay := base
	for count := uint(1); count < failures && delay < maximum; count++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	window := delay / 4
	offset := jitter(window)
	if offset < 0 {
		offset = 0
	}
	if offset > window {
		offset %= window + 1
	}
	return delay - offset
}
