package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/yetone/magpie/internal/sandbox"
)

// `magpie sandbox serve` is the broker: the one program beside magpie with
// a container engine's socket. magpie itself never holds one — it asks this
// program (internal/sandbox) to run a command in a box.
//
//	sandbox serve [--addr 127.0.0.1:8794] [--engine docker] [--work DIR]
//	sandbox status            what the plan is
//	sandbox on|off            whether an agent may ask for a box
//
// The plan is sandbox.json in magpie's config folder, edited from the
// window too (GET/POST /api/sandbox).
const sandboxUsage = `usage:
  magpie sandbox serve [--addr host:port] [--engine docker|podman] [--work DIR]
  magpie sandbox status
  magpie sandbox on|off`

func sandboxCmd(args []string) error {
	cmd := "status"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "status":
		p, err := sandbox.Load()
		if err != nil {
			return err
		}
		state := "off"
		if p.On {
			state = "on"
		}
		fmt.Printf("%s · %s · network %s · %d MB / %d CPUs / %ds · at most %d boxes\n",
			state, p.Image, p.Network, p.Limits.MemoryMB, p.Limits.CPUs, p.Limits.Seconds, p.Limits.MaxBoxes)
		fmt.Println("broker", p.Broker)
		return nil
	case "on", "off":
		p, err := sandbox.Load()
		if err != nil {
			return err
		}
		p.On = cmd == "on"
		if err := sandbox.Save(p); err != nil {
			return err
		}
		fmt.Println("sandbox", cmd)
		return nil
	case "serve":
		return sandboxServe(args)
	}
	return errors.New(sandboxUsage)
}

// sandboxServe reads the plan, takes what the flags say over it, and runs
// the broker until interrupted.
func sandboxServe(args []string) error {
	p, err := sandbox.Load()
	if err != nil {
		return err
	}
	addr := strings.TrimPrefix(strings.TrimPrefix(p.Broker, "http://"), "https://")
	engine := ""
	work := os.Getenv("MAGPIE_SANDBOX_WORK")
	// i is advanced past a flag's value, so it is not a plain range
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--addr":
			if i+1 >= len(args) {
				return errors.New("--addr needs host:port")
			}
			addr = args[i+1]
			i++
		case "--engine":
			if i+1 >= len(args) {
				return errors.New("--engine needs docker or podman")
			}
			engine = args[i+1]
			i++
		case "--work":
			if i+1 >= len(args) {
				return errors.New("--work needs a folder")
			}
			work = args[i+1]
			i++
		default:
			return errors.New(sandboxUsage)
		}
	}
	// the broker listens on loopback unless the user said otherwise: a
	// broker on the network is a daemon on the network
	b := &sandbox.Broker{Plan: p, Engine: engine, Work: work}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("sandbox broker on %s · %s · network %s · at most %d boxes\n",
		addr, p.Image, p.Network, p.Limits.MaxBoxes)
	return b.Serve(ctx, addr)
}
