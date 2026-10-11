package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The backend runs in a process of its own: this same program, started
// again with -backend. Its renders and scans, and its garbage collection
// of large image buffers, then never take the window's time, and it runs
// at a lower priority than the window, which a single process cannot do
// without starving Go's own scheduler. The window talks to it as it would
// to another machine's marraw: WebSocket for calls, HTTP for images.

// backendTokenEnv carries the launch token to the backend process, kept
// out of its command line, which other programs can read.
const backendTokenEnv = "MARRAW_BACKEND_TOKEN"

// backendReady is the line the backend process writes on its standard
// output once it listens, with the address after it.
const backendReady = "MARRAW_BACKEND "

// runBackendProcess is this program as the backend: it listens on
// loopback, on port if not nought, says where on its standard output, and
// stops when its standard input closes, as it does when the window's
// process ends, however it ends.
func runBackendProcess(dataDir string, port int) error {
	lowerPriority()
	token := os.Getenv(backendTokenEnv)
	if token == "" {
		return errors.New("-backend needs its token in " + backendTokenEnv)
	}
	_ = os.Unsetenv(backendTokenEnv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, stop, err := startBackend(ctx, dataDir, token, port)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, backendReady+addr)
	// The window's end closes the pipe; so does Ctrl+C in the console the
	// two share, which reaches both.
	_, _ = io.Copy(io.Discard, os.Stdin)
	log.Print("backend: the window is gone; stopping")
	stop()
	return nil
}

// backendProc is the backend process the window started.
type backendProc struct {
	dataDir, token string
	port           int
	addr           string

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.Closer
	exited   chan struct{}
	stopping bool
	// onRestart, when set, hears that the backend stopped and was
	// started again, and why.
	onRestart func(why string)
}

// spawnBackend starts the backend process and waits for it to listen.
func spawnBackend(ctx context.Context, dataDir, token string) (*backendProc, error) {
	b := &backendProc{dataDir: dataDir, token: token}
	if err := b.start(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

// start starts the process, on the port it had before if it had one, so
// the window's connection finds it again.
func (b *backendProc) start(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"-backend"}
	if b.dataDir != "" {
		args = append(args, "-data-dir", b.dataDir)
	}
	if b.port != 0 {
		args = append(args, "-backend-port", strconv.Itoa(b.port))
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), backendTokenEnv+"="+b.token)
	cmd.Stderr = os.Stderr
	childAttrs(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tieToThisProcess(cmd.Process)
	ready := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		said := false
		for sc.Scan() {
			line := sc.Text()
			if !said && strings.HasPrefix(line, backendReady) {
				said = true
				ready <- strings.TrimPrefix(line, backendReady)
				continue
			}
			fmt.Fprintln(os.Stdout, line)
		}
		if !said {
			close(ready)
		}
	}()
	select {
	case addr, ok := <-ready:
		if !ok {
			_ = cmd.Wait()
			return fmt.Errorf("the backend stopped before it listened (%v)", cmd.ProcessState)
		}
		b.mu.Lock()
		b.cmd, b.stdin, b.addr, b.exited = cmd, stdin, addr, make(chan struct{})
		if _, p, err := splitPort(addr); err == nil {
			b.port = p
		}
		b.mu.Unlock()
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		return errors.New("the backend did not start listening within two minutes")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return ctx.Err()
	}
	b.mu.Lock()
	exited := b.exited
	b.mu.Unlock()
	go b.watch(ctx, cmd, exited)
	return nil
}

// watch waits on the process, and starts it again if it stopped while the
// window still wants it, a few times at most.
func (b *backendProc) watch(ctx context.Context, cmd *exec.Cmd, exited chan struct{}) {
	err := cmd.Wait()
	close(exited)
	b.mu.Lock()
	stopping := b.stopping || ctx.Err() != nil
	b.mu.Unlock()
	if stopping {
		return
	}
	why := fmt.Sprintf("The backend stopped (%v)", err)
	if err == nil {
		why = "The backend stopped"
	}
	log.Print(why + "; starting it again")
	for try := 0; try < 3; try++ {
		if err := b.start(ctx); err == nil {
			if b.onRestart != nil {
				b.onRestart(why + ", and was started again.")
			}
			return
		} else {
			log.Printf("backend: start again: %v", err)
		}
		time.Sleep(time.Second)
	}
	if b.onRestart != nil {
		b.onRestart(why + ", and could not be started again. Restart marraw.")
	}
}

// stop ends the backend: its standard input closes, which it takes as the
// word to stop, and after a while it is killed.
func (b *backendProc) stop() {
	b.mu.Lock()
	b.stopping = true
	cmd, stdin, exited := b.cmd, b.stdin, b.exited
	b.mu.Unlock()
	if cmd == nil {
		return
	}
	_ = stdin.Close()
	select {
	case <-exited:
	case <-time.After(6 * time.Second):
		_ = cmd.Process.Kill()
	}
}

// splitPort is host:port's port.
func splitPort(addr string) (string, int, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", 0, errors.New("no port in " + addr)
	}
	p, err := strconv.Atoi(addr[i+1:])
	return addr[:i], p, err
}
