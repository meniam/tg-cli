package daemon

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// dashboardEventsWindow is the span of the "events in the last hour" counter.
const dashboardEventsWindow = time.Hour

// throttleQueueReporter is implemented by a client that sits behind the account
// throttle and can tell how many requests wait in it.
type throttleQueueReporter interface{ ThrottleQueued() int }

// dashboard answers MethodDashboard. It reads the state of the daemon and the
// local database and makes no Telegram request: the client is only asked for
// what it already knows (the connection state, the flood pause, the queue). It
// works without a login, like the status. The answer has chat ids and numbers,
// never a title or a text of Telegram.
func (d *daemon) dashboard(ctx context.Context, _ json.RawMessage) (any, error) {
	policy := d.policy()
	stats, err := d.store.DashboardStats(ctx, chatFilter(policy), time.Now().Add(-dashboardEventsWindow))
	if err != nil {
		return nil, err
	}
	version, err := d.store.Version()
	if err != nil {
		return nil, err
	}
	out := rpc.Dashboard{
		Profile: d.opts.Profile,
		PID:     os.Getpid(),
		Access: rpc.AccessStatus{
			Enabled: policy.Enabled(), Allow: policy.AllowCount(), Block: policy.BlockCount(), Write: policy.WriteCount(),
			Admin: policy.AdminCount(), Create: policy.CreateAllowed(), Profile: policy.ProfileAllowed(),
			Dialogs: policy.DialogsAllowed(), Sessions: policy.SessionsAllowed(), Closed: policy.Closed(),
		},
		Database:       rpc.DashboardDatabase{SchemaVersion: version, Chats: stats.Chats, Messages: stats.Messages},
		History:        dashboardHistory(stats),
		Background:     d.backgroundSyncStatus(),
		SemanticSearch: d.semanticIndexStatus(),
		Jobs:           d.dashboardJobs(),
		Events:         rpc.DashboardEvents{LastHour: stats.EventsSince, Total: stats.EventsTotal},
	}
	if !d.startedAt.IsZero() {
		out.StartedAt = d.startedAt.Unix()
	}
	// A file that cannot be read leaves the size at zero: the rest is still true.
	if info, err := os.Stat(d.store.Path()); err == nil {
		out.Database.SizeBytes = info.Size()
	}
	d.account.mu.RLock()
	if d.account.user != nil {
		user := *d.account.user
		out.Account.User = &user
		out.Account.Authorized = d.getAuthLost() == ""
	}
	d.account.mu.RUnlock()
	d.fillDashboardClient(ctx, &out)
	return out, nil
}

func dashboardHistory(stats store.DashboardStats) rpc.DashboardHistory {
	group := func(g int) rpc.DashboardGroup {
		return rpc.DashboardGroup{Complete: stats.History[g].Complete, Total: stats.History[g].Total}
	}
	out := rpc.DashboardHistory{
		People:  group(store.BackgroundGroupPeople),
		Folders: group(store.BackgroundGroupFolders),
		Others:  group(store.BackgroundGroupOther),
	}
	for _, g := range []rpc.DashboardGroup{out.People, out.Folders, out.Others} {
		out.Complete += g.Complete
		out.Total += g.Total
	}
	return out
}

// dashboardJobs lists the active history loads and the state of the full-data job.
func (d *daemon) dashboardJobs() rpc.DashboardJobs {
	jobs := rpc.DashboardJobs{Sync: []rpc.DashboardSyncJob{}}
	for _, job := range d.sync.snapshots() {
		switch job.State {
		case rpc.HistorySyncQueued, rpc.HistorySyncRunning, rpc.HistorySyncWaitingFlood:
			jobs.Sync = append(jobs.Sync, rpc.DashboardSyncJob{ChatID: job.ChatID, State: job.State, Source: job.Source, Pages: job.Pages, Loaded: job.Loaded})
		}
	}
	full := d.full.status()
	jobs.Full = rpc.DashboardFullJob{State: full.State, Done: full.Done + full.Failed, Total: full.Total}
	return jobs
}

// fillDashboardClient adds what the Telegram client knows without a request:
// the connection, the flood pause and the queue of the throttle. The lease only
// keeps the client from being replaced while it is read. A client that is not
// available is reported as the status reports it.
func (d *daemon) fillDashboardClient(ctx context.Context, out *rpc.Dashboard) {
	client := d.opts.Telegram
	if d.opts.NewTelegram != nil {
		_, leased, release, err := d.acquireTelegram(ctx)
		if err != nil {
			info := d.unavailableConnection()
			out.Connection = dashboardConnection(info)
			return
		}
		defer release()
		client = leased
	}
	if info, ok := d.connectionOf(client); ok {
		out.Connection = dashboardConnection(info)
	}
	if remaining := client.FloodWaitRemaining(); remaining > 0 {
		out.Throttle.FloodWaitSeconds = int((remaining + time.Second - 1) / time.Second)
	}
	if queue, ok := client.(throttleQueueReporter); ok {
		out.Throttle.Queued = queue.ThrottleQueued()
	}
}

func dashboardConnection(info rpc.ConnectionInfo) rpc.DashboardConnection {
	return rpc.DashboardConnection{Connection: info.Connection, Transport: info.Transport, ActiveTransport: info.ActiveTransport, DC: info.DC, Reconnects: info.Reconnects}
}
