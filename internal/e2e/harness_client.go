//go:build e2e_test

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// harnessClient drives one hush-web headless client (e2e/harness/client.mjs)
// running in its own OS process via vite-node. Commands are written as JSON
// lines to the child's stdin; events are read as JSON lines from its stdout.
// stderr is forwarded to the test log for diagnostics.
type harnessClient struct {
	t      *testing.T
	role   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan map[string]any
}

// startHarnessClient spawns one client process. The harness is executed through
// the hush-web project's local vite-node so the real src/lib modules resolve
// with the app's own Vite config (path aliases, extensionless imports).
func startHarnessClient(t *testing.T, ctx context.Context, webDir, role, baseURL, wsURL, channelID string) *harnessClient {
	t.Helper()
	bin := filepath.Join(webDir, "node_modules", ".bin", "vite-node")
	cmd := exec.CommandContext(ctx, bin, "e2e/harness/client.mjs", "--",
		"--role", role,
		"--base-url", baseURL,
		"--ws-url", wsURL,
		"--channel", channelID)
	cmd.Dir = webDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("%s: stdin pipe: %v", role, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("%s: stdout pipe: %v", role, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("%s: stderr pipe: %v", role, err)
	}
	if startErr := cmd.Start(); startErr != nil {
		t.Fatalf("%s: start %s: %v", role, bin, startErr)
	}

	c := &harnessClient{t: t, role: role, cmd: cmd, stdin: stdin, events: make(chan map[string]any, 64)}
	go c.scanEvents(stdout)
	go c.forwardStderr(stderr)
	return c
}

// scanEvents parses one JSON event object per stdout line onto the events
// channel, closing it when the stream ends so awaiters observe EOF.
func (c *harnessClient) scanEvents(stdout io.Reader) {
	defer close(c.events)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			c.t.Logf("[%s stdout non-json] %s", c.role, string(line))
			continue
		}
		c.events <- ev
	}
}

func (c *harnessClient) forwardStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		c.t.Logf("[%s stderr] %s", c.role, scanner.Text())
	}
}

// send writes one command object as a JSON line to the client's stdin.
func (c *harnessClient) send(cmd map[string]any) {
	c.t.Helper()
	payload, err := json.Marshal(cmd)
	if err != nil {
		c.t.Fatalf("%s: marshal command: %v", c.role, err)
	}
	if _, err := c.stdin.Write(append(payload, '\n')); err != nil {
		c.t.Fatalf("%s: write command %v: %v", c.role, cmd["cmd"], err)
	}
}

// await consumes events until one named `event` arrives and returns it. A
// `fatal` or `error` event, a closed stream, or the timeout fails the test:
// those are never the expected outcome on the happy path. Any other interleaved
// event (e.g. the async `frame_key` emitted while converging) is logged and
// skipped.
func (c *harnessClient) await(event string, timeout time.Duration) map[string]any {
	c.t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				c.t.Fatalf("%s: event stream closed before %q", c.role, event)
			}
			name, _ := ev["event"].(string)
			switch name {
			case event:
				return ev
			case "fatal", "error":
				c.t.Fatalf("%s: %s while awaiting %q: %v", c.role, name, event, ev)
			default:
				c.t.Logf("[%s event] %s (awaiting %q)", c.role, name, event)
			}
		case <-deadline:
			c.t.Fatalf("%s: timeout after %s awaiting %q", c.role, timeout, event)
		}
	}
}

// stop tells the client to exit cleanly, then kills it if it lingers.
func (c *harnessClient) stop() {
	c.send(map[string]any{"cmd": "exit"})
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
}
