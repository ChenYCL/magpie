// Package sandbox is the box an agent's untrusted work runs in: a
// container magpie starts for the occasion, runs a command in, and throws
// away. Reverse engineering, exploits being built, a binary nobody wrote
// down — none of it runs on magpie's own filesystem or with its own network
// reach.
//
// # Where the privilege sits
//
// magpie itself never talks to a container engine. It POSTs a command to a
// program beside it (the broker, `magpie sandbox serve`) that owns the
// Docker socket; that program starts the container, waits for the command,
// and answers with what came back. So a command an agent runs cannot reach
// magpie's keys, its /config, or this process's memory by way of the
// daemon, and the broker is the only thing with a socket to guard.
//
//	POST <broker>/exec    {"command":"…","image":"…"}
//	  → {"stdout":"…","stderr":"…","exit":0,"container":"…","truncated":false}
//
// A box is started from an image the user picked, with the limits the
// plan sets: no network unless asked, a memory and CPU ceiling, a read-only
// root with a writable /work, and `--rm` so a box that ended is gone. At
// most Max boxes live at once; the oldest idle one goes when a new one is
// needed (a box in a command is never taken).
//
// # Failing closed
//
// A broker that is down is not a box that is off: Exec answers ErrDown and
// the caller says so, naming the setup step (`magpie sandbox serve`), rather
// than running the command anywhere else. A box that exits non-zero is not
// a magpie failure: its exit code and output are the answer.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/appdir"
)

// File is the plan, by name in magpie's config folder.
const File = "sandbox.json"

// DefaultBroker is where `magpie sandbox serve` listens unless the plan
// says otherwise: magpie's loopback, a port of its own.
const DefaultBroker = "http://127.0.0.1:8794"

// DefaultImage has to hold a shell and the usual tooling; anything without
// `sh` cannot run a command line.
const DefaultImage = "debian:12-slim"

// ErrDown is what a call answers when no broker answers: the box is not
// off, its host is missing.
var ErrDown = errors.New("no sandbox broker answers: run `magpie sandbox serve` beside magpie (or start the sandbox service), and a command runs in a box instead of here")

// Limits are what a box may use: the plan's, and the broker's to enforce.
type Limits struct {
	MemoryMB int `json:"memoryMB"` // a box's memory ceiling, MB
	CPUs     int `json:"cpus"`     // the cores a box may use at once
	Seconds  int `json:"seconds"`  // how long one command may run
	MaxBoxes int `json:"maxBoxes"` // boxes alive at once (2–4)
}

// Plan is what the box is: the settings the page edits, kept in
// sandbox.json beside magpie's other files.
type Plan struct {
	On      bool   `json:"on"`      // whether an agent may ask for a box
	Broker  string `json:"broker"`  // where the broker listens
	Image   string `json:"image"`   // what a box is started from
	Network string `json:"network"` // "none" (default), "egress", or "host"
	Limits  Limits `json:"limits"`
}

// Default is the plan an install starts from: off, because a broker is
// something the user has to start.
func Default() Plan {
	return Plan{
		On:      false,
		Broker:  DefaultBroker,
		Image:   DefaultImage,
		Network: "none",
		Limits:  Limits{MemoryMB: 1024, CPUs: 2, Seconds: 300, MaxBoxes: 2},
	}
}

// Path is the plan's file.
func Path() string { return filepath.Join(appdir.Config(), File) }

// Load reads the plan, or Default when no file was written yet. A file
// that doesn't parse is an error, not a silent default.
func Load() (Plan, error) {
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), err
	}
	p := Default()
	if err := json.Unmarshal(b, &p); err != nil {
		return Default(), fmt.Errorf("%s: %w", Path(), err)
	}
	p.Normalize()
	return p, nil
}

// Normalize holds the plan to what a box may be: a broker URL on loopback,
// an image name, one of the two networks, and boxes between MinBoxes and 4.
func (p *Plan) Normalize() {
	if p.Broker == "" {
		p.Broker = DefaultBroker
	}
	if p.Image == "" {
		p.Image = DefaultImage
	}
	switch p.Network {
	case "none", "egress", "host":
	default:
		p.Network = "none"
	}
	if p.Limits.MaxBoxes == 0 {
		p.Limits.MaxBoxes = 2
	}
	if p.Limits.MaxBoxes > 4 {
		p.Limits.MaxBoxes = 4
	}
	if p.Limits.MaxBoxes < 1 {
		p.Limits.MaxBoxes = 1
	}
	if p.Limits.MemoryMB <= 0 {
		p.Limits.MemoryMB = 1024
	}
	if p.Limits.CPUs <= 0 {
		p.Limits.CPUs = 2
	}
	if p.Limits.Seconds <= 0 {
		p.Limits.Seconds = 300
	}
}

// Save writes the plan, normalized first.
func Save(p Plan) error {
	p.Normalize()
	if err := validateBroker(p.Broker); err != nil {
		return err
	}
	if err := validateImage(p.Image); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, Path()); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// validateBroker holds the broker to a loopback URL: a broker elsewhere
// would put the box's daemon on the network, which this layer is not.
func validateBroker(u string) error {
	parsed, err := url.Parse(u)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("the broker's URL is http:// or https://")
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("the broker is on this computer only: 127.0.0.1 or localhost")
}

// imageName is what a container engine takes as an image: a name rendered
// without spaces or shell metacharacters.
func validateImage(s string) error {
	if s == "" {
		return errors.New("an image is needed")
	}
	if strings.ContainsAny(s, " \t\n\r;|&$`\"'\\") {
		return errors.New("an image name has no spaces or shell characters")
	}
	return nil
}

// Exec runs one command in a box and answers what it printed.
type Exec struct {
	Command string `json:"command"`
	// Image is a box from another image than the plan's (an experiment's
	// own), empty for the plan's. It is held to the same name rule.
	Image string `json:"image,omitempty"`
}

// Result is what a command in a box answered.
type Result struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Exit      int    `json:"exit"`
	Container string `json:"container,omitempty"`
	Truncated bool   `json:"truncated,omitempty"` // the broker cut output at its cap
}

// Client asks a broker to run commands. It is the gateway tool's way in.
type Client struct {
	HTTP *http.Client
}

// NewClient is a client that gives a command the plan's time plus a wait
// for the box to start.
func NewClient() *Client { return &Client{HTTP: &http.Client{Timeout: 0}} }

// Run runs a command in a box, under a plan. It refuses when the plan is
// off (ErrOff) so a caller says "the box is off" rather than running the
// command here.
func (c *Client) Run(ctx context.Context, p Plan, in Exec) (Result, error) {
	p.Normalize()
	if !p.On {
		return Result{}, ErrOff
	}
	if strings.TrimSpace(in.Command) == "" {
		return Result{}, errors.New("a command is needed")
	}
	if in.Image != "" {
		if err := validateImage(in.Image); err != nil {
			return Result{}, err
		}
	}
	body, err := json.Marshal(in)
	if err != nil {
		return Result{}, err
	}
	u := strings.TrimRight(p.Broker, "/") + "/exec"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.Limits.Seconds+120)*time.Second)
	defer cancel()
	resp, err := hc.Do(req.WithContext(ctx))
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) || errors.Is(err, context.DeadlineExceeded) {
			return Result{}, fmt.Errorf("%w (%v)", ErrDown, err)
		}
		return Result{}, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("sandbox broker: %s: %s", resp.Status, strings.TrimSpace(string(out)))
	}
	var res Result
	if err := json.Unmarshal(out, &res); err != nil {
		return Result{}, fmt.Errorf("sandbox broker: %w", err)
	}
	return res, nil
}

// ErrOff is what Run answers when the plan is off.
var ErrOff = errors.New("the sandbox is off: turn it on in Settings › Sandbox to run this in a box")

// SetBroker points a plan at a broker: a test's, or a user who moved it.
func SetBroker(p *Plan, u string) { p.Broker = u }
