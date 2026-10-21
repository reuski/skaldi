// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func attachIPC(t *testing.T, c *IPCClient, conn net.Conn) {
	t.Helper()
	if err := c.installConn(conn); err != nil {
		t.Fatalf("installConn failed: %v", err)
	}
}

func TestIPCCloseReleasesReaderSaturatedOnEvents(t *testing.T) {
	c := NewIPCClient("unused", silentLogger())
	clientConn, serverConn := net.Pipe()
	attachIPC(t, c, clientConn)
	defer func() { _ = serverConn.Close() }()

	for i := 0; i < cap(c.Events); i++ {
		c.Events <- Event{Event: "property-change", Name: "pause"}
	}

	consumed := make(chan struct{})
	go func() {
		defer close(consumed)
		_, _ = serverConn.Write([]byte(`{"event":"property-change","name":"pause"}` + "\n"))
	}()

	select {
	case <-consumed:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader never consumed the event line")
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		c.Close()
	}()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked while the reader was publishing into a full Events buffer")
	}
}

func TestIPCReconnectUsesNewConnectionAndRetiresOld(t *testing.T) {
	c := NewIPCClient("unused", silentLogger())
	defer c.Close()

	oldClient, oldServer := net.Pipe()
	attachIPC(t, c, oldClient)
	defer func() { _ = oldServer.Close() }()

	newClient, newServer := net.Pipe()
	attachIPC(t, c, newClient)
	defer func() { _ = newServer.Close() }()

	if _, err := oldServer.Write([]byte("{}\n")); err == nil {
		t.Fatal("writing to the retired socket succeeded, want a closed connection")
	}

	type execResult struct {
		data any
		err  error
	}
	done := make(chan execResult, 1)
	go func() {
		data, err := c.Exec("get_property", "pause")
		done <- execResult{data, err}
	}()

	reader := bufio.NewReader(newServer)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the request failed: %v", err)
	}
	var cmd Command
	if err := json.Unmarshal([]byte(line), &cmd); err != nil {
		t.Fatalf("request is not a command: %v (%q)", err, line)
	}
	if len(cmd.Command) != 2 || cmd.Command[0] != "get_property" || cmd.RequestID == 0 {
		t.Fatalf("command = %#v, want the Exec arguments with a request id", cmd.Command)
	}

	if _, err := fmt.Fprintf(newServer, "{\"request_id\":%d,\"error\":\"success\",\"data\":{\"ok\":true}}\n", cmd.RequestID); err != nil {
		t.Fatalf("replying failed: %v", err)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("Exec failed on the new connection: %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Exec did not receive the reply sent on the new connection")
	}

	if _, err := newServer.Write([]byte(`{"event":"end-file","reason":"error","playlist_entry_id":3}` + "\n")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	select {
	case ev := <-c.Events:
		if ev.Event != "end-file" || ev.Reason != "error" || ev.PlaylistEntryID != 3 {
			t.Fatalf("event = %#v, want the end-file event from the new connection", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the event published on the new connection")
	}
}

func TestIPCExecReturnsWhenConnectionCloses(t *testing.T) {
	c := NewIPCClient("unused", silentLogger())
	clientConn, serverConn := net.Pipe()
	attachIPC(t, c, clientConn)
	defer func() { _ = serverConn.Close() }()

	done := make(chan error, 1)
	go func() {
		_, err := c.Exec("get_property", "pause")
		done <- err
	}()

	if _, err := bufio.NewReader(serverConn).ReadString('\n'); err != nil {
		t.Fatalf("reading the request failed: %v", err)
	}

	start := time.Now()
	c.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %s with a pending command", elapsed)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Exec succeeded, want an error once the connection closed")
		}
	case <-time.After(execTimeout + time.Second):
		t.Fatal("Exec did not return when the connection closed")
	}
}

func TestIPCExecConnectCloseRace(t *testing.T) {
	c := NewIPCClient("unused", silentLogger())

	var responderWG sync.WaitGroup
	connect := func() {
		client, server := net.Pipe()
		if err := c.installConn(client); err != nil {
			_ = server.Close()
			return
		}
		responderWG.Add(1)
		go func() {
			defer responderWG.Done()
			scanner := bufio.NewScanner(server)
			for scanner.Scan() {
				var cmd Command
				if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil || cmd.RequestID == 0 {
					continue
				}
				_, _ = fmt.Fprintf(server, "{\"request_id\":%d,\"error\":\"success\"}\n", cmd.RequestID)
			}
		}()
	}

	connect()

	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 15 {
				_, _ = c.Exec("get_property", "pause")
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				connect()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
		c.Close()
	}()

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("Exec, reconnect and Close did not all finish")
	}

	c.Close()

	responders := make(chan struct{})
	go func() {
		responderWG.Wait()
		close(responders)
	}()
	select {
	case <-responders:
	case <-time.After(5 * time.Second):
		t.Fatal("connection readers did not exit after Close")
	}
}
