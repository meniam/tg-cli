package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/schema"
)

func pipeSubscriptionServer(t *testing.T, server *Server) (net.Conn, *bufio.Reader, <-chan struct{}) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() { server.ServeConn(serverSide); close(done) }()
	t.Cleanup(func() { _ = clientSide.Close(); <-done; _ = server.Close() })
	return clientSide, bufio.NewReader(clientSide), done
}

func sendPipeRequest(t *testing.T, conn net.Conn, id int, method string, params any) {
	t.Helper()
	raw, err := marshalParams(params)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(Request{JSONRPC: Version, ID: json.RawMessage(strconv.Itoa(id)), Method: method, Params: raw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

func readPipeFrame(t *testing.T, conn net.Conn, reader *bufio.Reader) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(line) {
		t.Fatalf("broken frame: %q", line)
	}
	return line
}

func readEvent(t *testing.T, conn net.Conn, reader *bufio.Reader) Event {
	t.Helper()
	line := readPipeFrame(t, conn, reader)
	var note Request
	if err := json.Unmarshal(line, &note); err != nil {
		t.Fatal(err)
	}
	if note.Method != MethodEvent || len(note.ID) != 0 {
		t.Fatalf("not a notification: %s", line)
	}
	var event Event
	if err := json.Unmarshal(note.Params, &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestSubscriptionReplaysThenLivesWithoutDuplicates(t *testing.T) {
	hub := NewHub()
	srv := NewServer(nil)
	srv.SetEventSource(hub, func(context.Context, SubscribeParams) (ReplayPlan, error) {
		hub.Publish(Event{ID: "2.0.0.0", Type: EventMessageNew, Time: "2026-11-14T00:00:00Z"})
		return ReplayPlan{Events: []Event{{ID: "1.0.0.0", Type: EventMessageNew, Time: "2026-11-14T00:00:00Z"}, {ID: "2.0.0.0", Type: EventMessageNew, Time: "2026-11-14T00:00:00Z"}}, Cursor: "2.0.0.0", Filter: EventFilter{Types: []string{EventMessageNew}}}, nil
	})
	conn, reader, _ := pipeSubscriptionServer(t, srv)
	sendPipeRequest(t, conn, 1, MethodSubscribe, SubscribeParams{After: "0.0.0.0"})
	var response Response
	if err := json.Unmarshal(readPipeFrame(t, conn, reader), &response); err != nil {
		t.Fatal(err)
	}
	if string(response.ID) != "1" || response.Error != nil {
		t.Fatalf("subscribe response: %+v", response)
	}
	var result SubscribeResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.SubscriptionID == "" || result.Cursor != "2.0.0.0" {
		t.Fatalf("result: %+v", result)
	}
	for _, want := range []string{"1.0.0.0", "2.0.0.0"} {
		if event := readEvent(t, conn, reader); event.ID != want {
			t.Errorf("event = %+v, want %s", event, want)
		}
	}
	hub.Publish(Event{ID: "", Type: EventTyping, Time: "2026-11-14T00:00:00Z"})
	hub.Publish(Event{ID: "3.0.0.0", Type: EventMessageNew, Time: "2026-11-14T00:00:00Z"})
	if event := readEvent(t, conn, reader); event.ID != "3.0.0.0" {
		t.Errorf("live event = %+v", event)
	}
	sendPipeRequest(t, conn, 2, MethodSubscribe, nil)
	response = Response{}
	if err := json.Unmarshal(readPipeFrame(t, conn, reader), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != CodeBusy {
		t.Errorf("second subscribe: %+v", response)
	}
	sendPipeRequest(t, conn, 3, MethodUnsubscribe, UnsubscribeParams{SubscriptionID: result.SubscriptionID})
	response = Response{}
	if err := json.Unmarshal(readPipeFrame(t, conn, reader), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil || !strings.Contains(string(response.Result), `"unsubscribed":true`) {
		t.Errorf("unsubscribe: %+v", response)
	}
	if hub.Subscribers() != 0 {
		t.Errorf("subscribers after unsubscribe = %d", hub.Subscribers())
	}
}

func TestSubscriptionOverflowSendsLaggedAndCloses(t *testing.T) {
	hub := NewHub()
	srv := NewServer(nil)
	srv.SetEventSource(hub, func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{Cursor: "0.0.0.0"}, nil })
	conn, reader, _ := pipeSubscriptionServer(t, srv)
	sendPipeRequest(t, conn, 1, MethodSubscribe, nil)
	_ = readPipeFrame(t, conn, reader)
	for i := 1; i <= SubscriberQueueSize+2; i++ {
		hub.Publish(Event{ID: strconv.Itoa(i) + ".0.0.0", Type: EventMessageNew, Time: "2026-11-14T00:00:00Z"})
	}
	var lagged Event
	lastDelivered := "0.0.0.0"
	for i := 0; i < SubscriberQueueSize+2; i++ {
		event := readEvent(t, conn, reader)
		if event.Type == EventLagged {
			lagged = event
			break
		}
		lastDelivered = event.ID
	}
	if lagged.Type != EventLagged {
		t.Fatal("lagged notification missing")
	}
	if hub.Subscribers() != 0 {
		t.Errorf("overflowed subscriber remained: %d", hub.Subscribers())
	}
	if lagged.ID != lastDelivered {
		t.Errorf("lagged cursor %q, last delivered %q", lagged.ID, lastDelivered)
	}
}

func TestConnectionCloseCancelsSubscription(t *testing.T) {
	hub := NewHub()
	srv := NewServer(nil)
	srv.SetEventSource(hub, func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{Cursor: "0.0.0.0"}, nil })
	conn, reader, done := pipeSubscriptionServer(t, srv)
	sendPipeRequest(t, conn, 1, MethodSubscribe, nil)
	_ = readPipeFrame(t, conn, reader)
	if hub.Subscribers() != 1 {
		t.Fatalf("subscribers=%d", hub.Subscribers())
	}
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("connection goroutine did not stop")
	}
	if hub.Subscribers() != 0 {
		t.Errorf("subscriber leaked: %d", hub.Subscribers())
	}
}

func TestConnectionCloseCancelsReplay(t *testing.T) {
	hub := NewHub()
	srv := NewServer(nil)
	started := make(chan struct{})
	srv.SetEventSource(hub, func(ctx context.Context, _ SubscribeParams) (ReplayPlan, error) {
		close(started)
		<-ctx.Done()
		return ReplayPlan{}, ctx.Err()
	})
	conn, _, done := pipeSubscriptionServer(t, srv)
	sendPipeRequest(t, conn, 1, MethodSubscribe, nil)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("replay did not start")
	}
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("replay goroutine did not stop")
	}
	if hub.Subscribers() != 0 {
		t.Errorf("replay subscriber leaked: %d", hub.Subscribers())
	}
}

func TestResponsesAndNotificationsKeepWholeFrames(t *testing.T) {
	hub := NewHub()
	srv := NewServer(nil)
	srv.SetEventSource(hub, func(context.Context, SubscribeParams) (ReplayPlan, error) { return ReplayPlan{Cursor: "0.0.0.0"}, nil })
	srv.Handle("echo", func(context.Context, json.RawMessage) (any, error) { return map[string]bool{"ok": true}, nil })
	conn, reader, _ := pipeSubscriptionServer(t, srv)
	sendPipeRequest(t, conn, 1, MethodSubscribe, nil)
	_ = readPipeFrame(t, conn, reader)
	go func() {
		for i := 1; i <= 20; i++ {
			hub.Publish(Event{ID: strconv.Itoa(i) + ".0.0.0", Type: EventMessageNew, Time: "2026-11-14T00:00:00Z"})
		}
	}()
	sendPipeRequest(t, conn, 2, "echo", nil)
	responses, events := 0, 0
	for responses < 1 || events < 20 {
		line := readPipeFrame(t, conn, reader)
		var frame struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(line, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Method == MethodEvent {
			events++
		} else if string(frame.ID) == "2" {
			responses++
		} else {
			t.Errorf("unexpected frame: %s", line)
		}
	}
}

func TestSubscriptionTypesDescribeEventAndCursor(t *testing.T) {
	fields := schema.Fields(reflect.TypeOf(Event{}))
	want := map[string]bool{"id": false, "type": false, "chat_id": false, "time": false, "data": true}
	for _, field := range fields {
		marked, ok := want[field.Path]
		if !ok {
			t.Errorf("unexpected event field %s", field.Path)
			continue
		}
		if field.Untrusted != marked {
			t.Errorf("event field %s untrusted=%v", field.Path, field.Untrusted)
		}
		delete(want, field.Path)
	}
	if len(want) > 0 {
		t.Errorf("missing event fields %v", want)
	}
	if got, err := CursorID("42.10.11.12"); err != nil || got != 42 {
		t.Errorf("cursor ID=%d err=%v", got, err)
	}
	if _, err := CursorID("bad.10.11.12"); err == nil {
		t.Error("bad cursor accepted")
	}
	result := schema.Fields(reflect.TypeOf(SubscribeResult{}))
	if len(result) != 2 || result[0].Path != "subscription_id" || result[1].Path != "cursor" {
		t.Errorf("subscribe result fields: %+v", result)
	}
}
