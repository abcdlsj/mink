package host

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1/deliveryv1connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	runtimev1 "github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/observability"
)

type Executor interface {
	Execute(context.Context, Execution) (Completion, error)
}

type EligibleExecutor interface {
	Eligible(context.Context, string) (bool, error)
}

type Execution struct {
	AgentID             string
	ComputerID          string
	DeliveryID          string
	RunID               string
	LaunchID            string
	Fence               uint64
	PlacementGeneration uint64
	Workspace           string
	SpaceID             string
	ThreadRootMessageID string
	BasisTargetSequence uint64
	CurrentInput        string
}

type triggerContext struct {
	spaceID             string
	threadRootMessageID string
	observedHead        uint64
	body                string
}

type Completion struct {
	Outcome           deliveryv1.RunOutcome
	Body              string
	MentionedAgentIDs []string
}

type DaemonConfig struct {
	ServerURL          string
	DataRoot           string
	State              *computerstate.State
	HTTPClient         *http.Client
	Executor           Executor
	HeartbeatInterval  time.Duration
	SnapshotInterval   time.Duration
	DeliveryInterval   time.Duration
	RunRenewInterval   time.Duration
	OutboxInterval     time.Duration
	RuntimeRenewBefore time.Duration
	RPCDeadline        time.Duration
	BackoffMax         time.Duration
	Now                func() time.Time
	RetryJitter        func(time.Duration) time.Duration
	Logger             *observability.Logger
}

type computerDaemonClient interface {
	HeartbeatComputer(context.Context, *connect.Request[computerv1.HeartbeatComputerRequest]) (*connect.Response[computerv1.HeartbeatComputerResponse], error)
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

type deliveryDaemonClient interface {
	ListDeliveries(context.Context, *connect.Request[deliveryv1.ListDeliveriesRequest]) (*connect.Response[deliveryv1.ListDeliveriesResponse], error)
	AcceptDelivery(context.Context, *connect.Request[deliveryv1.AcceptDeliveryRequest]) (*connect.Response[deliveryv1.AcceptDeliveryResponse], error)
	ClaimRun(context.Context, *connect.Request[deliveryv1.ClaimRunRequest]) (*connect.Response[deliveryv1.ClaimRunResponse], error)
	RenewRun(context.Context, *connect.Request[deliveryv1.RenewRunRequest]) (*connect.Response[deliveryv1.RenewRunResponse], error)
	CompleteRun(context.Context, *connect.Request[deliveryv1.CompleteRunRequest]) (*connect.Response[deliveryv1.CompleteRunResponse], error)
}

type runWorker struct {
	cancel      context.CancelFunc
	agentID     string
	generation  uint64
	launchID    string
	fence       uint64
	leaseExpiry *atomic.Int64
}

type Daemon struct {
	config     DaemonConfig
	computers  computerDaemonClient
	placements placementDaemonClient
	runtimes   runtimeDaemonClient
	inbox      inboxDaemonClient
	deliveries deliveryDaemonClient

	workersMu sync.Mutex
	workers   map[string]runWorker
	workersWG sync.WaitGroup

	persistOutbox func(context.Context, computerstate.OutboxEvent) error

	lifecycleLogger *observability.Logger
	transportLogger *observability.Logger
	placementLogger *observability.Logger
	runtimeLogger   *observability.Logger
	deliveryLogger  *observability.Logger
	driverLogger    *observability.Logger
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
		deliveries:      deliveryv1connect.NewDeliveryServiceClient(client, config.ServerURL),
		workers:         make(map[string]runWorker),
		lifecycleLogger: observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryLifecycle),
		transportLogger: observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryTransport),
		placementLogger: observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryPlacement),
		runtimeLogger:   observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryRuntime),
		deliveryLogger:  observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryDelivery),
		driverLogger:    observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryDriver),
		outboxLogger:    observability.CategoryLogger(config.Logger, observability.ComponentComputer, observability.CategoryOutbox),
	}
}

func (d *Daemon) Run(ctx context.Context) error {
	if d.config.State == nil {
		return errors.New("computer state is required")
	}
	identity, found, err := d.config.State.Identity(ctx)
	if err != nil {
		return err
	}
	if !found || identity.ServerURL != d.config.ServerURL {
		return errors.New("computer identity is unavailable for this Server")
	}
	d.lifecycleLogger.Info("computer daemon started", "event", "computer.daemon.started", "computer_id", identity.ComputerID, "server_origin", identity.ServerURL, "executor_enabled", d.config.Executor != nil)
	var loops sync.WaitGroup
	loops.Add(3)
	go func() {
		defer loops.Done()
		d.connectivitySupervisor(ctx, identity)
	}()
	go func() {
		defer loops.Done()
		d.deliveryLoop(ctx, identity)
	}()
	go func() {
		defer loops.Done()
		d.outboxLoop(ctx)
	}()
	<-ctx.Done()
	d.lifecycleLogger.Info("computer daemon shutdown requested", "event", "computer.daemon.shutdown.requested", "reason", context.Cause(ctx))
	d.stopAllWorkers()
	loops.Wait()
	d.workersWG.Wait()
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
	if config.DeliveryInterval <= 0 {
		config.DeliveryInterval = time.Second
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
