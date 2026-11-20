package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

const notificationWriteTimeout = 5 * time.Second

func (sc *serverConn) stopSubscription() {
	sc.mu.Lock()
	sub := sc.sub
	sc.sub, sc.subID, sc.pending = nil, "", false
	sc.mu.Unlock()
	if sub != nil {
		sub.Close()
	}
}

func (sc *serverConn) clearSubscription(sub *Subscription) {
	sc.mu.Lock()
	if sc.sub == sub {
		sc.sub, sc.subID, sc.pending = nil, "", false
	}
	sc.mu.Unlock()
	sub.Close()
}

func (s *Server) subscribe(ctx context.Context, sc *serverConn, req Request) (Response, *Subscription, ReplayPlan) {
	s.mu.Lock()
	hub, replay := s.hub, s.replay
	s.mu.Unlock()
	if hub == nil || replay == nil {
		return errorResponse(req.ID, CodeMethodNotFound, "method not found: subscribe"), nil, ReplayPlan{}
	}
	var params SubscribeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorResponse(req.ID, CodeInvalidParams, "invalid subscribe params"), nil, ReplayPlan{}
		}
	}
	if params.Topic < 0 {
		return errorResponse(req.ID, CodeInvalidParams, "topic must not be negative"), nil, ReplayPlan{}
	}
	if _, err := CursorID(params.After); err != nil {
		return errorResponse(req.ID, CodeInvalidParams, "invalid event cursor"), nil, ReplayPlan{}
	}
	sc.mu.Lock()
	if sc.sub != nil || sc.pending {
		sc.mu.Unlock()
		return errorResponse(req.ID, CodeBusy, "this connection already has a subscription"), nil, ReplayPlan{}
	}
	sc.pending = true
	sub := hub.Subscribe()
	id := strconv.FormatUint(s.nextSub.Add(1), 10)
	sc.sub, sc.subID = sub, id
	sc.mu.Unlock()

	replayCtx, cancel := context.WithTimeout(ctx, s.RequestTimeout)
	plan, err := replay(replayCtx, params)
	cancel()
	if err != nil || ctx.Err() != nil {
		sc.clearSubscription(sub)
		var rpcErr *Error
		if errors.As(err, &rpcErr) {
			return errorResponse(req.ID, rpcErr.Code, rpcErr.Message), nil, ReplayPlan{}
		}
		return errorResponse(req.ID, CodeInternal, "event replay failed"), nil, ReplayPlan{}
	}
	if plan.Cursor == "" {
		plan.Cursor = params.After
	}
	plan.resume = params.After
	cursorID, cursorErr := CursorID(plan.Cursor)
	afterID, _ := CursorID(params.After)
	previous := afterID
	for _, event := range plan.Events {
		id, err := CursorID(event.ID)
		if err != nil || id <= previous || id > cursorID {
			cursorErr = errors.New("invalid replay event order")
			break
		}
		previous = id
	}
	if cursorErr != nil {
		sc.clearSubscription(sub)
		return errorResponse(req.ID, CodeInternal, "event replay returned an invalid cursor or order"), nil, ReplayPlan{}
	}
	sub.SetFilter(plan.Filter)
	sc.mu.Lock()
	if sc.sub != sub {
		sc.mu.Unlock()
		sub.Close()
		return errorResponse(req.ID, CodeInternal, "subscription closed"), nil, ReplayPlan{}
	}
	sc.pending = false
	sc.mu.Unlock()
	result, err := json.Marshal(SubscribeResult{SubscriptionID: id, Cursor: plan.Cursor})
	if err != nil {
		sc.clearSubscription(sub)
		return errorResponse(req.ID, CodeInternal, "encode subscription result"), nil, ReplayPlan{}
	}
	return Response{JSONRPC: Version, ID: req.ID, Result: result}, sub, plan
}

func (s *Server) unsubscribe(sc *serverConn, req Request) Response {
	var params UnsubscribeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorResponse(req.ID, CodeInvalidParams, "invalid unsubscribe params")
		}
	}
	sc.mu.Lock()
	if sc.pending {
		sc.mu.Unlock()
		return errorResponse(req.ID, CodeBusy, "subscription is starting")
	}
	if sc.sub != nil && params.SubscriptionID != "" && params.SubscriptionID != sc.subID {
		sc.mu.Unlock()
		return errorResponse(req.ID, CodeInvalidParams, "subscription id does not match")
	}
	sub := sc.sub
	sc.sub, sc.subID = nil, ""
	sc.mu.Unlock()
	if sub != nil {
		sub.Close()
	}
	result, _ := json.Marshal(UnsubscribeResult{Unsubscribed: sub != nil})
	return Response{JSONRPC: Version, ID: req.ID, Result: result}
}

func (s *Server) sendEvents(ctx context.Context, sc *serverConn, w *frameWriter, sub *Subscription, plan ReplayPlan) {
	defer sc.clearSubscription(sub)
	lastID, _ := CursorID(plan.Cursor)
	lastDelivered := plan.resume
	if len(plan.Events) == 0 {
		lastDelivered = plan.Cursor
	}
	if plan.resume == "" {
		// The live queue was registered before the starting cursor was read.
		// It may contain events at or below that cursor, and they must be sent.
		lastID = 0
		// Before the first delivery a lagged subscriber resumes from the starting
		// cursor: "0.0.0.0" would read as an expired cursor.
		lastDelivered = plan.Cursor
	}
	for _, event := range plan.Events {
		if stopped(ctx, sub) {
			return
		}
		if lagged(sub) {
			s.sendLagged(w, lastDelivered)
			return
		}
		if err := s.notify(w, event); err != nil {
			_ = sc.c.Close()
			return
		}
		lastDelivered = event.ID
	}
	for {
		if lagged(sub) {
			s.sendLagged(w, lastDelivered)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-sub.Done():
			return
		case <-sub.Lagged():
			s.sendLagged(w, lastDelivered)
			return
		case event := <-sub.Events():
			if !sub.accepts(event) {
				continue
			}
			id, err := CursorID(event.ID)
			if err != nil {
				continue
			}
			if id > 0 && id <= lastID {
				continue
			}
			if err := s.notify(w, event); err != nil {
				_ = sc.c.Close()
				return
			}
			if id > 0 {
				lastID = id
			}
			if event.ID != "" {
				lastDelivered = event.ID
			}
		}
	}
}

func stopped(ctx context.Context, sub *Subscription) bool {
	select {
	case <-ctx.Done():
		return true
	case <-sub.Done():
		return true
	default:
		return false
	}
}

func lagged(sub *Subscription) bool {
	select {
	case <-sub.Lagged():
		return true
	default:
		return false
	}
}

func (s *Server) sendLagged(w *frameWriter, cursor string) {
	_ = s.notify(w, Event{ID: cursor, Type: EventLagged, Time: time.Now().UTC().Format(time.RFC3339)})
}

// notify writes one server-to-client JSON-RPC notification without an ID.
// The frame writer serializes it with ordinary responses on this connection.
func (s *Server) notify(w *frameWriter, event Event) error {
	if s.eventGuard != nil && !s.eventGuard(event) {
		return nil
	}
	params, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return w.writeWithTimeout(Request{JSONRPC: Version, Method: MethodEvent, Params: params}, notificationWriteTimeout)
}
