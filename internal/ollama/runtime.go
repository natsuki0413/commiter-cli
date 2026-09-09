package ollama

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/natsuki0413/commiter-cli/internal/config"
)

const (
	startupTimeout = 15 * time.Second
	probeInterval  = 100 * time.Millisecond
	shutdownGrace  = 2 * time.Second
)

type Runtime struct {
	Client *Client
	owned  managedProcess
	closed chan struct{}
	once   sync.Once
}

type managedProcess interface {
	Signal(os.Signal) error
	Kill() error
	Done() <-chan error
}

type processStarter func(*urlEndpoint) (managedProcess, error)

type urlEndpoint struct {
	host string
}

func Open(ctx context.Context, values config.Values) (*Runtime, error) {
	client, err := New(values)
	if err != nil {
		return nil, err
	}
	return open(ctx, client, startDaemon)
}

func open(ctx context.Context, client *Client, starter processStarter) (*Runtime, error) {
	if err := client.Compatibility(ctx); err == nil {
		if err := requireModel(ctx, client); err != nil {
			return nil, err
		}
		return &Runtime{Client: client, closed: make(chan struct{})}, nil
	} else if !isConnectionRefused(err) || ctx.Err() != nil {
		return nil, err
	}

	process, err := starter(&urlEndpoint{host: client.endpoint.Host})
	if err != nil {
		return nil, llmError("Ollama is not installed or could not be started")
	}
	runtime := &Runtime{Client: client, owned: process, closed: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = runtime.Close()
		case <-runtime.closed:
		}
	}()

	startupContext, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	for {
		if err := client.Compatibility(startupContext); err == nil {
			if err := requireModel(startupContext, client); err != nil {
				_ = runtime.Close()
				return nil, err
			}
			return runtime, nil
		} else if !isTransportError(err) {
			_ = runtime.Close()
			return nil, err
		}
		select {
		case <-startupContext.Done():
			_ = runtime.Close()
			return nil, llmError("timed out waiting for the local Ollama API")
		case <-process.Done():
			_ = runtime.Close()
			return nil, llmError("the owned Ollama daemon stopped before becoming ready")
		case <-time.After(probeInterval):
		}
	}
}

func requireModel(ctx context.Context, client *Client) error {
	present, err := client.HasModel(ctx)
	if err != nil {
		return err
	}
	if !present {
		return llmError("configured Ollama model is not installed; run setup before retrying")
	}
	return nil
}

func isTransportError(err error) bool {
	var target transportError
	return errors.As(err, &target)
}

func isConnectionRefused(err error) bool {
	return isTransportError(err) && errors.Is(err, syscall.ECONNREFUSED)
}

func (r *Runtime) OwnedDaemon() bool {
	return r != nil && r.owned != nil
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	var closeErr error
	r.once.Do(func() {
		close(r.closed)
		if r.owned == nil {
			return
		}
		if err := r.owned.Signal(os.Interrupt); err != nil {
			select {
			case <-r.owned.Done():
				return
			default:
			}
			closeErr = err
		}
		timer := time.NewTimer(shutdownGrace)
		defer timer.Stop()
		select {
		case <-r.owned.Done():
		case <-timer.C:
			if err := r.owned.Kill(); err != nil && closeErr == nil {
				closeErr = err
			}
			<-r.owned.Done()
		}
	})
	return closeErr
}

type execProcess struct {
	process *os.Process
	done    chan error
}

func startDaemon(endpoint *urlEndpoint) (managedProcess, error) {
	path, err := exec.LookPath("ollama")
	if err != nil {
		return nil, err
	}
	command := exec.Command(path, "serve")
	command.Env = replaceEnv(os.Environ(), "OLLAMA_HOST", endpoint.host)
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, err
	}
	process := &execProcess{process: command.Process, done: make(chan error, 1)}
	go func() {
		process.done <- command.Wait()
		close(process.done)
	}()
	return process, nil
}

func (p *execProcess) Signal(signal os.Signal) error { return p.process.Signal(signal) }
func (p *execProcess) Kill() error                   { return p.process.Kill() }
func (p *execProcess) Done() <-chan error            { return p.done }

func replaceEnv(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}
