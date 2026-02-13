package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type IPCClient struct {
	socketPath string
	conn       net.Conn
	logger     *slog.Logger

	nextReqID uint64
	pending   map[uint64]chan Response
	pendingMu sync.Mutex

	Events chan Event

	quit chan struct{}
	wg   sync.WaitGroup
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
	Event string      `json:"event"`
	Name  string      `json:"name"`
	Data  interface{} `json:"data"`
}

func NewIPCClient(socketPath string, logger *slog.Logger) *IPCClient {
	return &IPCClient{
		socketPath: socketPath,
		logger:     logger,
		pending:    make(map[uint64]chan Response),
		Events:     make(chan Event, 100),
		quit:       make(chan struct{}),
	}
}

func (c *IPCClient) Connect() error {
	var conn net.Conn
	var err error

	for i := 0; i < 10; i++ {
		conn, err = net.Dial("unix", c.socketPath)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to mpv socket after retries: %w", err)
	}

	c.conn = conn

	c.wg.Add(1)
	go c.readLoop()

	return nil
}

func (c *IPCClient) Close() {
	close(c.quit)
	if c.conn != nil {
		c.conn.Close()
	}
	c.wg.Wait()
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

	if _, err := c.conn.Write(data); err != nil {
		return nil, fmt.Errorf("write failed: %w", err)
	}

	select {
	case resp := <-respChan:
		if resp.Error != "success" && resp.Error != "" {
			return nil, fmt.Errorf("mpv error: %s", resp.Error)
		}
		return resp.Data, nil
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timeout waiting for response")
	}
}

func (c *IPCClient) readLoop() {
	defer c.wg.Done()
	scanner := bufio.NewScanner(c.conn)

	for scanner.Scan() {
		line := scanner.Bytes()

		var msg struct {
			RequestID uint64      `json:"request_id"`
			Error     string      `json:"error"`
			Data      interface{} `json:"data"`
			Event     string      `json:"event"`
			Name      string      `json:"name"`
		}

		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if msg.Event != "" {
			c.Events <- Event{
				Event: msg.Event,
				Name:  msg.Name,
				Data:  msg.Data,
			}
		} else {
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
	}

	c.logger.Debug("IPC read loop exited")
}
