# Hot-Reload (Zero-Downtime Upgrade) Issue Analysis

## Problem Summary

The current `self_update` tool with `SIGUSR2` hot-reload mechanism has critical bugs that prevent it from working correctly.

## Code Flow

```
self_update tool:
  1. go install ./cmd/mink/          # Build new binary
  2. sm.FlushAll()                   # Persist sessions
  3. syscall.Kill(pid, SIGUSR2)      # Trigger upgrade

daemon.handleSignals():
  SIGUSR2 → d.upgrade()

daemon.upgrade():
  1. Get executable path
  2. Get listener fd from UnixListener
  3. exec.Command(bin, "serve", "--inherit-fd=3")
  4. cmd.ExtraFiles = []*os.File{fd}  # Pass fd to child
  5. cmd.Start()                       # Start new process
  6. time.Sleep(100ms)
  7. d.close()                         # ❌ BUG: closes shared fd!
  8. os.Exit(0)
```

## Bug 1: Inherited FD Closed by Parent

**Location**: `daemon.go:upgrade()` → `d.close()`

```go
func (d *Daemon) close() {
    // ...
    if d.ln != nil {
        _ = d.ln.Close()  // ← Closes the shared fd!
    }
}
```

When parent calls `close()` before `os.Exit(0)`, it closes the listener fd that was passed to the child process via `ExtraFiles`. The child inherits the same underlying file description, so when parent closes it, child's listener becomes invalid.

**Impact**: New process cannot accept connections, fails with "use of closed network connection".

## Bug 2: systemd Restart Race

**Current systemd config**:
```ini
[Service]
Restart=always
RestartSec=1
```

`Restart=always` restarts the service on **any** exit condition, including:
- Clean exit (exit 0)
- Unclean exit (crash, signal)

When hot-reload triggers:
1. Old process calls `os.Exit(0)`
2. systemd sees exit(0) with `Restart=always` → triggers restart
3. New process (from hot-reload) + systemd restart = **two instances**
4. Socket conflict: "address already in use"

**Impact**: Either socket conflict or double restart, depending on timing.

## Root Cause Analysis

### Why fd inheritance fails

In Unix, `ExtraFiles` uses `dup2()` to pass fd to child. But:
- Parent's `d.ln` is a `*net.UnixListener` wrapping the fd
- `d.ln.Close()` calls `close(fd)` on the underlying fd
- Child inherited the same fd number, but it's the same kernel file description
- Parent's `close()` invalidates it for both processes

### Why systemd restarts

`Restart=always` means:
```
service exits for ANY reason → restart
```

Hot-reload expects:
```
service exits via upgrade → do NOT restart (child is the new service)
```

These are incompatible.

## Potential Fixes

### Option 1: Skip close() on upgrade

```go
func (d *Daemon) upgrade() {
    // ... start child ...
    
    d.upgrading = true  // Mark upgrading
    time.Sleep(100 * time.Millisecond)
    // Skip d.close(), directly exit
    os.Exit(0)
}

func (d *Daemon) close() {
    if d.upgrading {
        return  // Don't close if upgrading
    }
    // ... normal close logic
}
```

**Problem**: Still conflicts with `Restart=always`.

### Option 2: Change systemd to Restart=on-failure

```ini
[Service]
Restart=on-failure  # Only restart on non-zero exit
RestartSec=1
```

With this, hot-reload's `os.Exit(0)` won't trigger restart.

**Problem**: If mink crashes (panic, OOM killer), exit code may be 0 or non-deterministic. `on-failure` might not catch all failure cases.

### Option 3: Use exec() instead of fork

Replace current process entirely:

```go
func (d *Daemon) upgrade() {
    // ... prepare fd ...
    
    syscall.Exec(bin, []string{"mink", "serve", "--inherit-fd=3"}, os.Environ())
    // Never returns on success
}
```

**Advantages**:
- No parent/child fd sharing issues
- Same PID, systemd sees no exit

**Disadvantages**:
- Must ensure fd stays open across exec()
- More complex state handover

### Option 4: Remove hot-reload, use systemd restart

Simplest approach: let systemd handle restarts.

```go
func (s *SelfUpdate) Run(...) {
    // ... build ...
    // ... flush sessions ...
    
    // Tell user to run: systemctl restart mink
    return "Build complete. Run: systemctl restart mink", nil
}
```

**Trade-off**: Loses zero-downtime, gains reliability.

## Recommendation

Short-term: **Option 2 + Option 1**
1. Change systemd to `Restart=on-failure`
2. Fix code to not close fd on upgrade

Long-term: **Option 3** for true zero-downtime, or **Option 4** for simplicity.

## Testing

To verify hot-reload works:
```bash
# Terminal 1: Start daemon
mink serve

# Terminal 2: Trigger upgrade
mink upgrade
# Or via self_update tool

# Check
systemctl status mink  # Should show single PID, no restart loops
lsof -p $(pgrep mink) | grep mink.sock  # Should show socket open
```

## Related Code

- `daemon.go:upgrade()` - Main upgrade logic
- `daemon.go:close()` - Cleanup that destroys shared fd
- `tool/self_update.go` - Tool that triggers upgrade
- `/etc/systemd/system/mink.service` - systemd config with `Restart=always`
