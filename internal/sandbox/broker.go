package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The broker: the one program with a container engine's socket. magpie
// starts it beside itself (`magpie sandbox serve`), it listens on loopback,
// and the only thing it will do is run a command in a fresh box.
//
// It is deliberately small. What it takes from a body: a command line and an
// image name. Everything else — the limits, the network, the mount — is the
// plan the broker read, not the caller's to choose, so a command cannot ask
// for `--privileged` and cannot mount the host's filesystem.

// Broker is the server. Socket is the container engine's endpoint; Engine is
// the binary (`docker`, or `podman` where the CLI matches).
type Broker struct {
	Plan   Plan
	Engine string // "docker" unless set
	// Work is the folder a box's /work is bound to on the host. Empty is
	// none: a box keeps its /work only for the command's life.
	Work string

	mu   sync.Mutex
	busy int // commands running now, against the plan's MaxBoxes
}

// Serve answers /exec, /boxes and /reset until ctx ends.
func (b *Broker) Serve(ctx context.Context, addr string) error {
	b.Plan.Normalize()
	if b.Engine == "" {
		b.Engine = "docker"
	}
	srv := &http.Server{Addr: addr, Handler: b.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// handler is the broker's routes: a test's httptest server, and Serve's.
func (b *Broker) handler() http.Handler {
	b.Plan.Normalize()
	if b.Engine == "" {
		b.Engine = "docker"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /exec", b.exec)
	mux.HandleFunc("GET /boxes", b.list)
	mux.HandleFunc("POST /reset", b.reset)
	return mux
}

func (b *Broker) exec(rw http.ResponseWriter, r *http.Request) {
	var in Exec
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		httpError(rw, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(in.Command) == "" {
		httpError(rw, http.StatusBadRequest, errors.New("a command is needed"))
		return
	}
	image := b.Plan.Image
	if in.Image != "" {
		if err := validateImage(in.Image); err != nil {
			httpError(rw, http.StatusBadRequest, err)
			return
		}
		image = in.Image
	}
	res, err := b.run(r.Context(), image, in.Command)
	if err != nil {
		httpError(rw, http.StatusInternalServerError, err)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(res)
}

func (b *Broker) list(rw http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	running, max := b.busy, b.Plan.Limits.MaxBoxes
	b.mu.Unlock()
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]any{"running": running, "max": max})
}

// reset takes away every box the broker started, whether or not a command
// still holds it: the page's Restore, so the next command starts from the
// image again. A box left by a magpie that died mid-command goes here too.
func (b *Broker) reset(rw http.ResponseWriter, r *http.Request) {
	label := "magpie-sandbox=1"
	out, err := exec.CommandContext(r.Context(), b.Engine, "ps", "-aq", "--filter", "label="+label).Output()
	if err != nil {
		httpError(rw, http.StatusInternalServerError, err)
		return
	}
	ids := strings.Fields(string(out))
	removed, failed := 0, []string{}
	for _, id := range ids {
		if o, err := exec.CommandContext(r.Context(), b.Engine, "rm", "-f", id).CombinedOutput(); err != nil {
			failed = append(failed, id+": "+strings.TrimSpace(string(o)))
			continue
		}
		removed++
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]any{"removed": removed, "failed": failed})
}

// run starts a box, runs the command, and takes the box away.
func (b *Broker) run(ctx context.Context, image, command string) (Result, error) {
	b.mu.Lock()
	if b.busy >= b.Plan.Limits.MaxBoxes {
		b.mu.Unlock()
		return Result{}, fmt.Errorf("all %d boxes are in use: wait for one to end", b.Plan.Limits.MaxBoxes)
	}
	b.busy++
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.busy--; b.mu.Unlock() }()

	ms := b.Plan.Limits.Seconds
	if ms <= 0 {
		ms = 300
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Second)
	defer cancel()

	args := []string{"run", "--rm", "-i", "--label", "magpie-sandbox=1",
		"--memory", strconv.Itoa(b.Plan.Limits.MemoryMB) + "m",
		"--cpus", strconv.Itoa(b.Plan.Limits.CPUs),
		"--pids-limit", "512",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--read-only",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
		"--workdir", "/work",
	}
	switch b.Plan.Network {
	case "egress":
		// the default bridge: out, and in to nothing on this computer
	case "host":
		args = append(args, "--network", "host")
	default:
		args = append(args, "--network", "none")
	}
	if b.Work != "" {
		// a folder the user gave the box: writable, and nothing else of the
		// host is (no docker socket, no /config)
		args = append(args, "-v", b.Work+":/work")
	} else {
		args = append(args, "--tmpfs", "/work:rw,nosuid,size=256m")
	}
	args = append(args, image, "sh", "-c", command)

	cmd := exec.CommandContext(ctx, b.Engine, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &capWriter{w: &stdout, left: outputCap}
	cmd.Stderr = &capWriter{w: &stderr, left: outputCap}
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if cmd.ProcessState != nil {
		res.Exit = cmd.ProcessState.ExitCode()
	}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.Exit = 124
		res.Stderr += fmt.Sprintf("\n[sandbox] the command ran past %ds and was ended\n", ms)
	case err != nil && res.Exit == 0:
		return res, err
	}
	return res, nil
}

// outputCap is what one stream may carry back: enough for a tool's report,
// not enough to fill magpie's memory.
const outputCap = 1 << 20

type capWriter struct {
	w    io.Writer
	left int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.left <= 0 {
		return len(p), nil // counted as written: the command is not stopped
	}
	if len(p) > c.left {
		p = p[:c.left]
	}
	c.left -= len(p)
	_, err := c.w.Write(p)
	return len(p), err
}

func httpError(rw http.ResponseWriter, code int, err error) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(map[string]string{"error": err.Error()})
}
