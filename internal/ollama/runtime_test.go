package ollama

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

func TestOpenReusesExistingDaemonAndDoesNotStopIt(t *testing.T) {
	client := readyClient()
	started := false
	runtime, err := open(context.Background(), client, func(*urlEndpoint) (managedProcess, error) {
		started = true
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if started || runtime.OwnedDaemon() {
		t.Fatalf("started = %v, owned = %v", started, runtime.OwnedDaemon())
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenStartsAndStopsOnlyOwnedDaemonOnContextCancellation(t *testing.T) {
	var mutex sync.Mutex
	ready := false
	client := testClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mutex.Lock()
		isReady := ready
		mutex.Unlock()
		if !isReady {
			return nil, connectionRefused()
		}
		return readyResponse(request), nil
	}))
	process := newFakeProcess()
	ctx, cancel := context.WithCancel(context.Background())
	runtime, err := open(ctx, client, func(endpoint *urlEndpoint) (managedProcess, error) {
		if endpoint.host != "127.0.0.1:11434" {
			t.Fatalf("daemon endpoint = %q", endpoint.host)
		}
		mutex.Lock()
		ready = true
		mutex.Unlock()
		return process, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.OwnedDaemon() {
		t.Fatal("OwnedDaemon() = false")
	}

	cancel()
	select {
	case <-process.signaled:
	case <-time.After(time.Second):
		t.Fatal("owned daemon was not signaled after cancellation")
	}
	if process.signal != os.Interrupt || process.killed {
		t.Fatalf("signal = %v, killed = %v", process.signal, process.killed)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDoesNotStartDaemonForNonRefusedTransportFailure(t *testing.T) {
	client := testClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	}))
	started := false
	_, err := open(context.Background(), client, func(*urlEndpoint) (managedProcess, error) {
		started = true
		return newFakeProcess(), nil
	})
	if err == nil || started {
		t.Fatalf("open() error = %v, started = %v", err, started)
	}
}

func readyClient() *Client {
	return testClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return readyResponse(request), nil
	}))
}

func readyResponse(request *http.Request) *http.Response {
	switch request.URL.Path {
	case "/api/version":
		return jsonResponse(http.StatusOK, `{"version":"0.12.6"}`)
	case "/api/tags":
		return jsonResponse(http.StatusOK, `{"models":[{"name":"model:latest","model":"model:latest"}]}`)
	default:
		return jsonResponse(http.StatusNotFound, `{}`)
	}
}

type fakeProcess struct {
	done     chan error
	signaled chan struct{}
	once     sync.Once
	signal   os.Signal
	killed   bool
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{done: make(chan error), signaled: make(chan struct{})}
}

func (process *fakeProcess) Signal(signal os.Signal) error {
	process.signal = signal
	process.once.Do(func() {
		close(process.signaled)
		close(process.done)
	})
	return nil
}

func (process *fakeProcess) Kill() error {
	process.killed = true
	process.once.Do(func() {
		close(process.done)
	})
	return nil
}

func (process *fakeProcess) Done() <-chan error { return process.done }
