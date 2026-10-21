// SPDX-License-Identifier: AGPL-3.0-or-later

package player

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	execTimeout      = 5 * time.Second
	connectAttempts  = 10
	connectRetryWait = 100 * time.Millisecond
)

var errIPCClosed = errors.New("mpv IPC connection is closed")

// Retirement must unblock both socket I/O and publication to a saturated Events buffer.
type ipcConn struct {
	conn net.Conn
	done chan struct{}
	once sync.Once
}

func newIPCConn(conn net.Conn) *ipcConn {
	return &ipcConn{conn: conn, done: make(chan struct{})}
}

func (ic *ipcConn) retire() {
	ic.once.Do(func() {
		close(ic.done)
		_ = ic.conn.Close()
	})
}

type IPCClient struct {
	socketPath string
	logger     *slog.Logger

	nextReqID uint64
	pending   map[uint64]chan Response
	pendingMu sync.Mutex

	writeMu sync.Mutex

	Events chan Event

	mu      sync.Mutex
	conn    *ipcConn
	closed  bool
	readers sync.WaitGroup
}

type Command struct {
	Command   []interface{} `json:"command"`
	RequestID uint64        `json:"request_id,omitempty"`
}

type Response struct {
	RequestID uint64      `json:"request_id"`
	Error     string      `json:"error"`
	Data      interface{} `json:"data"`
}

type Event struct {
	Event           string      `json:"event"`
	Name            string      `json:"name"`
	Data            interface{} `json:"data"`
	Reason          string      `json:"reason,omitempty"`
	PlaylistEntryID int         `json:"playlist_entry_id,omitempty"`
}

func NewIPCClient(socketPath string, logger *slog.Logger) *IPCClient {
	return &IPCClient{
		socketPath: socketPath,
		logger:     logger,
		pending:    make(map[uint64]chan Response),
		Events:     make(chan Event, 100),
	}
}

func (c *IPCClient) Connect() error {
	var conn net.Conn
	var err error

	for i := 0; i < connectAttempts; i++ {
		conn, err = net.Dial("unix", c.socketPath)
		if err == nil {
			break
		}
		time.Sleep(connectRetryWait)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to mpv socket after retries: %w", err)
	}

	return c.installConn(conn)
}

// Register readers under the close lock so Add cannot race Wait.
func (c *IPCClient) installConn(conn net.Conn) error {
	ic := newIPCConn(conn)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return errIPCClosed
	}
	previous := c.conn
	c.conn = ic
	c.readers.Add(1)
	c.mu.Unlock()

	if previous != nil {
		previous.retire()
	}
	go c.readLoop(ic)

	return nil
}

func (c *IPCClient) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn != nil {
		conn.retire()
	}
	c.readers.Wait()
}

func (c *IPCClient) activeConn() *ipcConn {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	return c.conn
}

func (c *IPCClient) Exec(args ...interface{}) (interface{}, error) {
	reqID := atomic.AddUint64(&c.nextReqID, 1)

	cmd := Command{
		Command:   args,
		RequestID: reqID,
	}

	respChan := make(chan Response, 1)
	c.pendingMu.Lock()
	c.pending[reqID] = respChan
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
	}()

	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal failed: %w", err)
	}

	data = append(data, '\n')

	conn := c.activeConn()
	if conn == nil {
		return nil, errIPCClosed
	}

	// Do not hold the connection lock during Write: retirement must unblock stalled I/O.
	c.writeMu.Lock()
	_, err = conn.conn.Write(data)
	c.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write failed: %w", err)
	}

	// Prefer delivered replies over shutdown or timeout.
	select {
	case resp := <-respChan:
		return responseData(resp)
	default:
	}

	timer := time.NewTimer(execTimeout)
	defer timer.Stop()

	select {
	case resp := <-respChan:
		return responseData(resp)
	case <-conn.done:
		return nil, errIPCClosed
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting for response")
	}
}

func responseData(resp Response) (interface{}, error) {
	if resp.Error != "success" && resp.Error != "" {
		return nil, fmt.Errorf("mpv error: %s", resp.Error)
	}
	return resp.Data, nil
}

func (c *IPCClient) readLoop(ic *ipcConn) {
	defer c.readers.Done()
	defer ic.retire()

	scanner := bufio.NewScanner(ic.conn)

	for scanner.Scan() {
		line := scanner.Bytes()

		var msg struct {
			RequestID       uint64      `json:"request_id"`
			Error           string      `json:"error"`
			Data            interface{} `json:"data"`
			Event           string      `json:"event"`
			Name            string      `json:"name"`
			Reason          string      `json:"reason"`
			PlaylistEntryID int         `json:"playlist_entry_id"`
		}

		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if msg.Event != "" {
			// Drop raw error text: it may contain authenticated stream URLs.
			event := Event{
				Event:           msg.Event,
				Name:            msg.Name,
				Data:            msg.Data,
				Reason:          msg.Reason,
				PlaylistEntryID: msg.PlaylistEntryID,
			}
			select {
			case c.Events <- event:
			case <-ic.done:
				c.logger.Debug("IPC read loop exited")
				return
			}
			continue
		}

		c.pendingMu.Lock()
		ch, ok := c.pending[msg.RequestID]
		c.pendingMu.Unlock()

		if ok {
			ch <- Response{
				RequestID: msg.RequestID,
				Error:     msg.Error,
				Data:      msg.Data,
			}
		}
	}

	c.logger.Debug("IPC read loop exited")
}
